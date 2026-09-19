package main

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
	"github.com/urfave/cli/v2"
	"golang.org/x/term"
)

// block is a finished command frozen as a cell grid.
type block struct {
	cells [][]vt10x.Glyph
	width int
	code  int
	hash  uint64
}

// session is the mutable state of a running rt session, kept in one place so
// the whole of it can be read at a glance. The pty reader and the stdin reader
// touch fields from other goroutines, so every access to mu, buffer, alt,
// first, final, finalCode and clearReq is serialized by mu. vt, history and
// dirty are only ever touched by the main goroutine, but live here too.
type session struct {
	mu        sync.Mutex
	buffer    []byte // pty bytes read but not yet fed to vt
	alt       bool   // shell is on the DEC 1049 alt screen
	first     bool   // the bootstrap prompt has not been seen yet
	final     bool   // pty EOF: flush the unfinished command as the last block
	finalCode int    // shell exit code, known once final is set
	clearReq  bool   // a swallowed ^L asked to clear history

	vt      vt10x.Terminal
	history []block
	dirty   bool
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

// run starts the shell in a pty, mirrors its output into a vt emulator, and
// repaints a frame made of frozen command blocks above the live prompt.
//
// Data flow:
//
//	pty master --holdIter--> st.buffer --tick/composeChunk--> st.vt
//	                                                        (frozen at each
//	                                                         prompt marker into
//	                                                         st.history)
//	os.Stdin --swallow--> pty master
//	st.vt + st.history --renderFrame--> os.Stdout
//
// Concurrency: the pty reader and the stdin reader run on their own goroutines;
// every shared field is serialized by st.mu. The main goroutine owns st.vt,
// st.history and st.dirty outright, and is the only one that repaints.
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

	st := &session{
		vt:    vt10x.New(vt10x.WithSize(width, height)),
		first: true,
		dirty: true,
	}

	// boundary runs when a prompt marker arrives: the just-finished command's
	// grid is frozen into history with its exit code (unless this is the first,
	// bootstrap, prompt), and the emulator is reset for the next command.
	boundary := func(code int) {
		log.event("sep")
		st.dirty = true
		if !st.first {
			g, _, _ := snapshotGrid(st.vt)
			if g != nil {
				b := newBlock(g, width, code)
				st.history = append(st.history, b)
				log.block(len(st.history), blockText(b.cells))
			}
		} else {
			st.first = false
		}
		st.vt = vt10x.New(vt10x.WithSize(width, height))
	}

	// compose feeds a buffer chunk into the emulator, re-buffering any split
	// marker head.
	compose := func(data []byte) {
		if left := composeChunk(&st.vt, data, boundary); len(left) > 0 {
			st.buffer = append(st.buffer, left...)
		}
	}

	// swallow intercepts a pure CTRL-l read at the idle prompt.
	swallow := func(data []byte) bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		if !swallowCtrlL(data, st.buffer, st.first, st.alt) {
			return false
		}
		st.clearReq = true
		return true
	}

	// repaint writes the whole frame to the real terminal.
	repaint := func() {
		os.Stdout.Write(renderFrame(st.history, st.vt, width, height))
	}

	// tick drains the buffer into vt at command boundaries, then repaints.
	tick := func() {
		st.mu.Lock()
		if st.clearReq {
			st.clearReq = false
			if len(st.history) > 0 {
				st.history = nil
				log.event("ctrll clear")
				st.dirty = true
			}
		}
		if !st.dirty && len(st.buffer) == 0 && !st.final {
			st.mu.Unlock()
			return
		}

		buf := st.buffer
		st.buffer = nil
		if len(buf) > 0 {
			compose(buf)
			st.dirty = true
		}

		// EOF: the final unfinished command becomes the last block.
		if st.final {
			g, _, _ := snapshotGrid(st.vt)
			if g != nil {
				b := newBlock(g, width, st.finalCode)
				st.history = append(st.history, b)
				log.event("block " + itoa(len(st.history)) + " (final)")
				log.block(len(st.history), blockText(b.cells))
				st.dirty = true
			}
			st.vt = vt10x.New(vt10x.WithSize(width, height))
		}

		if st.dirty {
			st.dirty = false
			st.mu.Unlock()
			repaint()
			return
		}
		st.mu.Unlock()
	}

	sigwinch := make(chan os.Signal, 1)
	signal.Notify(sigwinch, syscall.SIGWINCH)
	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGTERM)
	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, syscall.SIGINT)

	renderTick := make(chan struct{}, 1)
	wake := func() {
		select {
		case renderTick <- struct{}{}:
		default:
		}
	}

	// Reader: pty -> buffer (never touches vt)
	go func() {
		// hold guarantees pty output never splits a prompt-marker head or an
		// alt-screen toggle across two chunks, so per-chunk detection is complete.
		hold := func(b []byte) int {
			return holdPrefixes(b, [][]byte{ansiSepHead, []byte(ansiEnterAltScreen), []byte(ansiLeaveAltScreen)})
		}
		for chunk, rerr := range holdIter(master, hold) {
			if len(chunk) > 0 {
				log.rx(chunk)
				st.mu.Lock()
				trackAltScreen(chunk, &st.alt)
				st.buffer = append(st.buffer, chunk...)
				st.mu.Unlock()
				wake()
			}
			if rerr != nil {
				// EOF from the pty: the shell has exited. Reap it here so the
				// final unfinished block can carry the shell's own exit code.
				state, _ := cmd.Process.Wait()
				st.mu.Lock()
				if state != nil {
					st.finalCode = state.ExitCode()
				}
				st.final = true
				st.mu.Unlock()
				wake()
				return
			}
		}
	}()

	// Reader: stdin -> pty
	go func() {
		buf := make([]byte, 32768)
		for {
			n, rerr := os.Stdin.Read(buf)
			if n > 0 {
				if swallow(buf[:n]) {
					log.event("ctrll")
					wake()
					continue
				}
				log.tx(buf[:n])
				_, _ = master.Write(buf[:n])
			}
			if rerr != nil {
				// stdin closed: stop forwarding, but let sh finish.
				return
			}
		}
	}()

	exit := false
	onRender := func() {
		tick()
	}
	onWinch := func() {
		width, height := termSize(os.Stdin)
		if width < 1 || height < 1 {
			return
		}
		_ = pty.Setsize(master, &pty.Winsize{Rows: uint16(height), Cols: uint16(width)})
		st.mu.Lock()
		st.vt.Resize(width, height)
		st.dirty = true
		st.mu.Unlock()
		tick()
	}
	onSigint := func() {
		log.event("SIGINT")
		_ = syscall.Kill(cmd.Process.Pid, syscall.SIGINT)
	}
	onSigterm := func() {
		log.event("SIGTERM")
		exit = true
	}
	// checkDone reports that the pty hit EOF and the final block has been
	// flushed into st.history; the select loop then stops.
	checkDone := func() bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		return st.final
	}

	for !exit {
		select {
		case <-renderTick:
			onRender()
		case <-sigwinch:
			onWinch()
		case <-sigint:
			onSigint()
		case <-sigterm:
			onSigterm()
		}
		if checkDone() {
			break
		}
	}

	log.event("exit")
	log.Close()
	_ = master.Close()
	st.mu.Lock()
	code := st.finalCode
	st.mu.Unlock()
	fmt.Fprintf(os.Stderr, "%s (%s) exited with code %d\n", shell, mode, code)
	return cli.Exit("", code)
}

func termSize(f *os.File) (w, h int) {
	w, h, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 80, 24
	}
	return w, h
}
