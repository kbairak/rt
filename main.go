package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
	"github.com/urfave/cli/v2"
	"golang.org/x/term"
)

// frameInterval caps how often the view repaints.
const frameInterval = time.Second / 30

// outMu serializes writes to the real terminal: the loop paints frames while
// the copy path may emit an OSC 52 clipboard sequence.
var outMu sync.Mutex

// block is a finished command frozen as a cell grid. count is the number of
// consecutive identical commands this entry represents; they are collapsed at
// append time to save memory.
type block struct {
	cells [][]vt10x.Glyph
	width int
	code  int
	count int
	hash  uint64
}

// session is the state of a running rt session plus the operations the three
// goroutines perform on it:
//
//   - forwardStdin: stdin -> pty
//   - readPty:      pty   -> buffer
//   - loop:         wakes on events, composes the buffer into vt, repaints
//
// buffer, alt, first, final, finalCode, clearReq, the copy-overlay fields and
// the current width/height are shared, so access to them is serialized by mu.
// vt, history and dirty are only ever touched by the loop goroutine, but live
// here too.
type session struct {
	mu        sync.Mutex
	buffer    []byte // pty bytes read but not yet fed to vt
	alt       bool   // shell is on the DEC 1049 alt screen
	first     bool   // the bootstrap prompt has not been seen yet
	final     bool   // pty EOF: flush the unfinished command as the last block
	finalCode int    // shell exit code, known once final is set
	clearReq  bool   // an intercepted ^L asked to clear history
	width     int    // current terminal width, updated on SIGWINCH
	height    int    // current terminal height, updated on SIGWINCH

	// Copy overlay. While copyActive, stdin is consumed by the overlay and pty
	// output stays in buffer; compose skips draining it. sel/scroll index view,
	// the filtered entry list (view == history when no filter is applied).
	copyActive   bool
	sel          int
	scroll       int
	view         []block // filtered entries copy-mode renders/navigates
	filter       string  // applied search query (empty = none)
	searchActive bool
	query        string
	searchPend   []byte
	collapsed    bool // collapse blocks to their first collapsedLines rows
	copyPend     []byte
	copyPending  bool
	copySel      int
	copyText     string

	vt      vt10x.Terminal
	history []block
	dirty   bool
	rend    renderer

	events     chan struct{} // loop wakeups (cap 1, coalesced)
	frameTimer *time.Timer   // trailing repaint after the rate-limit window
	lastFrame  time.Time

	log    *recorder
	master *os.File
	cmd    *exec.Cmd
}

func main() {
	app := &cli.App{
		Name:      "rt",
		Usage:     "reverse-terminal: pin the shell prompt to the top",
		UsageText: "rt [options] [shell]",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name: "mode", Aliases: []string{"m"},
				Usage: "shell mode: sh|zsh|bash|python (default: inferred from shell)",
			},
		},
		Action: run,
	}
	if err := app.Run(os.Args); err != nil {
		if ec, ok := err.(cli.ExitCoder); ok {
			os.Exit(ec.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "rt:", err)
		os.Exit(1)
	}
}

// run sets up the pty and the terminal, then hands off to three goroutines: two
// that shuttle bytes (stdin -> pty and pty -> buffer) and the loop, which wakes
// when something changed and repaints at most once per frameInterval.
func run(c *cli.Context) error {
	if os.Getenv("REVERSE_TERMINAL") != "" {
		return cli.Exit("nested rt unsupported", 1)
	}

	shell := resolveShell(c)
	mode, err := resolveMode(c)
	if err != nil {
		return err
	}

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("stdin is not a terminal")
	}
	restore, err := prepareTerm()
	if err != nil {
		return err
	}
	defer restore()

	width, height := termSize(os.Stdin)
	if width < 1 || height < 1 {
		width, height = 80, 24
	}

	log, err := newRecorder()
	if err != nil {
		return fmt.Errorf("recorder: %w", err)
	}

	cmd, cleanup, err := getCmd(shell, mode)
	if err != nil {
		return fmt.Errorf("rc: %w", err)
	}
	defer cleanup()

	master, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(height), Cols: uint16(width)})
	if err != nil {
		return fmt.Errorf("pty: %w", err)
	}

	s := &session{
		vt:      vt10x.New(vt10x.WithSize(width, height)),
		first:   true,
		dirty:   true,
		width:   width,
		height:  height,
		copySel: -1,
		events:  make(chan struct{}, 1),
		log:     log,
		master:  master,
		cmd:     cmd,
	}

	go s.forwardStdin() // stdin -> pty
	go s.readPty()      // pty   -> buffer
	s.loop()            // wake on events, render at most once per frameInterval

	log.event("exit")
	log.Close()
	_ = master.Close()
	s.mu.Lock()
	code := s.finalCode
	s.mu.Unlock()
	fmt.Fprintf(os.Stderr, "%s (%s) exited with code %d\n", shell, mode, code)
	return cli.Exit("", code)
}

// forwardStdin is the stdin -> pty goroutine. Reads are routed by routeStdin:
// copy-mode consumes them, a pure ^^ at the idle prompt opens the overlay, a
// pure ^L at the idle prompt clears history, and everything else goes to the
// shell.
func (s *session) forwardStdin() {
	buf := make([]byte, 32768)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			s.routeStdin(buf[:n])
		}
		if err != nil {
			// stdin closed: stop forwarding, but let the shell finish.
			return
		}
	}
}

// routeStdin delivers one stdin read to exactly one destination. The idle gate
// (empty buffer, past the bootstrap prompt, not in alt-screen) is what keeps
// the copy trigger from ever shadowing a running command or a fullscreen app.
func (s *session) routeStdin(data []byte) {
	s.mu.Lock()
	if s.copyActive {
		s.mu.Unlock()
		s.handleCopyInput(data)
		return
	}
	if onlyHasCopyKeys(data) &&
		len(s.buffer) == 0 && !s.first && !s.alt && len(s.history) > 0 {
		s.copyActive = true
		s.view = s.history
		s.filter = ""
		s.searchActive = false
		s.query = ""
		s.searchPend = nil
		s.collapsed = false
		s.sel = len(s.history) - 1
		s.scroll = 0
		s.copyPend = nil
		s.dirty = true
		s.mu.Unlock()
		s.wake()
		return
	}
	if onlyHasCtrlLs(data) && len(s.buffer) == 0 && !s.first && !s.alt {
		s.clearReq = true
		s.mu.Unlock()
		s.log.event("ctrll")
		s.wake()
		return
	}
	s.mu.Unlock()
	s.log.tx(data)
	_, _ = s.master.Write(data)
}

// readPty is the pty -> buffer goroutine. hold guarantees a prompt-marker head
// or alt-screen toggle is never split across chunks, so per-chunk detection is
// complete. On EOF it reaps the shell so the final block carries its exit code.
func (s *session) readPty() {
	hold := func(b []byte) int {
		return holdPrefixes(b, [][]byte{ansiSepHead, []byte(ansiEnterAltScreen), []byte(ansiLeaveAltScreen)})
	}
	for chunk, err := range holdIter(s.master, hold) {
		if len(chunk) > 0 {
			s.log.rx(chunk)
			s.mu.Lock()
			trackAltScreen(chunk, &s.alt)
			s.buffer = append(s.buffer, chunk...)
			s.mu.Unlock()
			s.wake()
		}
		if err != nil {
			state, _ := s.cmd.Process.Wait()
			s.mu.Lock()
			if state != nil {
				s.finalCode = state.ExitCode()
			}
			s.final = true
			s.mu.Unlock()
			s.wake()
			return
		}
	}
}

// wake pings the loop without blocking; multiple pings collapse into one.
func (s *session) wake() {
	select {
	case s.events <- struct{}{}:
	default:
	}
}

// loop is the main goroutine. It waits for something interesting (pty output,
// the frame timer, a resize, or a signal) and then renders if due.
func (s *session) loop() {
	sigwinch := make(chan os.Signal, 1)
	signal.Notify(sigwinch, syscall.SIGWINCH)
	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, syscall.SIGINT)
	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGTERM)

	s.frameTimer = time.NewTimer(frameInterval)
	if !s.frameTimer.Stop() {
		<-s.frameTimer.C
	}

	for {
		select {
		case <-s.events:
		case <-s.frameTimer.C:
		case <-sigwinch:
			s.resize()
		case <-sigint:
			s.log.event("SIGINT")
			_ = syscall.Kill(s.cmd.Process.Pid, syscall.SIGINT)
		case <-sigterm:
			s.log.event("SIGTERM")
			return
		}
		s.flushCopy()
		if s.renderMaybe() && s.done() {
			return
		}
	}
}

// renderMaybe repaints, but at most once per frameInterval: the first event
// after a quiet spell paints immediately (leading edge); a burst is capped and
// gets one trailing paint when the window elapses. Reports whether it painted.
func (s *session) renderMaybe() bool {
	if !s.needsRender() {
		return false
	}
	if rem := frameInterval - time.Since(s.lastFrame); rem > 0 {
		s.frameTimer.Reset(rem)
		return false
	}
	s.compose()
	s.paint()
	s.lastFrame = time.Now()
	return true
}

// needsRender reports whether the frame is out of date. While the copy overlay
// is active only an explicit dirty flag forces a repaint: pty output is held in
// buffer and must not update the frozen frame.
func (s *session) needsRender() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.copyActive {
		return s.dirty
	}
	return s.dirty || len(s.buffer) > 0 || s.final || s.clearReq
}

// compose turns staged pty bytes into emulator state: it drains the buffer into
// vt, freezing a command at each prompt marker, flushes the final command at
// EOF, then leaves the frame marked clean. While the copy overlay is active the
// buffer is deliberately left untouched so nothing changes behind the overlay.
func (s *session) compose() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.copyActive {
		s.dirty = false
		return
	}

	if s.clearReq {
		s.clearReq = false
		if len(s.history) > 0 {
			s.history = nil
			s.log.event("ctrll clear")
			s.dirty = true
		}
	}
	if buf := s.buffer; len(buf) > 0 {
		s.buffer = nil
		if left := composeChunk(&s.vt, buf, s.freezeCommand); len(left) > 0 {
			s.buffer = append(s.buffer, left...)
		}
		s.dirty = true
	}
	if s.final {
		s.freezeFinal()
	}
	s.dirty = false
}

// freezeCommand freezes the just-finished command's grid into history with its
// exit code (except the bootstrap prompt) and resets the emulator. Caller holds
// s.mu.
func (s *session) freezeCommand(code int) {
	s.log.event("sep")
	s.dirty = true
	if !s.first {
		if g, _, _ := snapshotGrid(s.vt); g != nil {
			b := newBlock(g, s.width, code)
			if s.appendBlock(b) {
				s.log.block(len(s.history), blockText(b.cells))
			}
		}
	} else {
		s.first = false
	}
	s.vt = vt10x.New(vt10x.WithSize(s.width, s.height))
}

// freezeFinal freezes the still-unfinished command as the last block when the
// pty hits EOF. Caller holds s.mu.
func (s *session) freezeFinal() {
	if g, _, _ := snapshotGrid(s.vt); g != nil {
		b := newBlock(g, s.width, s.finalCode)
		if s.appendBlock(b) {
			s.log.event("block " + itoa(len(s.history)) + " (final)")
			s.log.block(len(s.history), blockText(b.cells))
		}
		s.dirty = true
	}
	s.vt = vt10x.New(vt10x.WithSize(s.width, s.height))
}

// appendBlock adds b to history, collapsing it into the newest entry when the
// grids are identical (incrementing that entry's count) instead of storing a
// duplicate. It reports whether a new entry was created. Caller holds s.mu.
func (s *session) appendBlock(b block) bool {
	if n := len(s.history); n > 0 && blocksEqual(s.history[n-1], b) {
		s.history[n-1].count++
		return false
	}
	s.history = append(s.history, b)
	return true
}

// paint writes the minimal frame that brings the terminal up to date.
func (s *session) paint() {
	s.mu.Lock()
	his, vt := s.history, s.vt
	w, h := s.width, s.height
	var ov *overlay
	if s.copyActive {
		his = s.view
		ov = &overlay{
			sel:       s.sel,
			scroll:    s.scroll,
			searching: s.searchActive,
			query:     s.query,
			filter:    s.filter,
			collapsed: s.collapsed,
		}
	}
	s.mu.Unlock()

	out := s.rend.frame(his, vt, w, h, ov)
	outMu.Lock()
	_, _ = os.Stdout.Write(out)
	outMu.Unlock()
}

// flushCopy performs a clipboard write requested by the overlay. It runs on the
// loop goroutine so the OSC 52 fallback shares outMu with painting.
func (s *session) flushCopy() {
	s.mu.Lock()
	if !s.copyPending {
		s.mu.Unlock()
		return
	}
	s.copyPending = false
	text := s.copyText
	s.copyText = ""
	s.mu.Unlock()
	setClipboard(text)
}

// resize applies a new terminal size to both the pty and the emulator, and
// records it so future blocks and frames use it. Existing history blocks keep
// the width they were created with and are cropped when rendered.
func (s *session) resize() {
	w, h := termSize(os.Stdin)
	if w < 1 || h < 1 {
		return
	}
	_ = pty.Setsize(s.master, &pty.Winsize{Rows: uint16(h), Cols: uint16(w)})
	s.mu.Lock()
	s.width, s.height = w, h
	s.vt.Resize(w, h)
	s.dirty = true
	s.mu.Unlock()
}

// done reports that the pty hit EOF, the final block has been flushed, and the
// loop can stop.
func (s *session) done() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.final
}

func termSize(f *os.File) (w, h int) {
	w, h, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 80, 24
	}
	return w, h
}
