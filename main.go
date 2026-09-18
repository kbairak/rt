package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
	"golang.org/x/term"
)

// sepPayload is the distinctive body of the zero-width OSC marker embedded in
// the shell prompt. A user cannot type it, so accidental matches are
// impossible.
const sepPayload = "RT;7f3a9b"

// sepHead opens the zero-width prompt marker; the shell appends the last
// command's exit code (digits) then sepEnd (BEL). A user cannot type it, so
// accidental matches are impossible.
var (
	sepHead = []byte("\x1b]" + sepPayload + ";")
	sepEnd  = byte(0x07)
)

// block is a finished command frozen as a cell grid.
type block struct {
	cells [][]vt10x.Glyph
	width int
	code  int
}

// rtState owns everything: buffer (stdin reader writes, renderer drains), the
// vt and history (renderer only), and the log recorder.
type rtState struct {
	sync.Mutex
	buffer    []byte
	vt        vt10x.Terminal
	width     int
	height    int
	his       []block
	log       *recorder
	proc      *exec.Cmd
	dirty     bool
	first     bool
	drain     bool
	finalCode int
}

func main() {
	interactive := term.IsTerminal(int(os.Stdin.Fd()))
	var restore = func() {}
	if interactive {
		f, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			fmt.Fprintf(os.Stderr, "raw: %v\n", err)
			os.Exit(1)
		}
		restore = func() {
			os.Stdout.WriteString("\x1b[?25h\x1b[0m")
			_ = term.Restore(int(os.Stdin.Fd()), f)
		}
	}
	defer restore()

	w, h := termSize(os.Stdin)
	if w < 1 || h < 1 {
		w, h = 80, 24
	}

	rec, err := newRecorder()
	if err != nil {
		fmt.Fprintln(os.Stderr, "recorder:", err)
		os.Exit(1)
	}

	script, err := writeRcScript()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rc:", err)
		os.Exit(1)
	}
	defer script.cleanup()

	cmd := exec.Command("sh")
	cmd.Env = append(os.Environ(), "ENV="+script.path)
	master, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(h), Cols: uint16(w)})
	if err != nil {
		fmt.Fprintln(os.Stderr, "pty:", err)
		os.Exit(1)
	}

	rt := &rtState{
		vt:     vt10x.New(vt10x.WithSize(w, h)),
		width:  w,
		height: h,
		log:    rec,
		proc:   cmd,
		dirty:  true,
		first:  true,
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
		buf := make([]byte, 32768)
		for {
			n, rerr := master.Read(buf)
			if n > 0 {
				rt.log.rx(buf[:n])
				rt.Lock()
				rt.buffer = append(rt.buffer, buf[:n]...)
				rt.Unlock()
				wake()
			}
			if rerr != nil {
				// EOF from the pty: the shell has exited. Reap it here so the
				// final unfinished block can carry the shell's own exit code.
				state, _ := cmd.Process.Wait()
				rt.Lock()
				if state != nil {
					rt.finalCode = state.ExitCode()
				}
				rt.drain = true
				rt.Unlock()
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
				rt.log.tx(buf[:n])
				_, _ = master.Write(buf[:n])
			}
			if rerr != nil {
				// stdin closed: stop forwarding, but let sh finish.
				return
			}
		}
	}()

	exit := false
	code := 0
	for !exit {
		select {
		case <-renderTick:
			rt.tick()
		case <-sigwinch:
			w, h = termSize(os.Stdin)
			if w < 1 || h < 1 {
				continue
			}
			_ = pty.Setsize(master, &pty.Winsize{Rows: uint16(h), Cols: uint16(w)})
			rt.Lock()
			rt.width, rt.height = w, h
			rt.vt.Resize(w, h)
			rt.dirty = true
			rt.Unlock()
			rt.tick()
		case <-sigint:
			rt.log.event("SIGINT")
			_ = syscall.Kill(cmd.Process.Pid, syscall.SIGINT)
		case <-sigterm:
			rt.log.event("SIGTERM")
			exit = true
		}
		rt.Lock()
		done := rt.drain
		code = rt.finalCode
		rt.Unlock()
		if done {
			exit = true
		}
	}

	rt.log.event("exit")
	rec.Close()
	_ = master.Close()
	fmt.Fprintf(os.Stderr, "sh exited with code %d\n", code)
	restore()
	os.Exit(code)
}

func termSize(f *os.File) (w, h int) {
	w, h, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 80, 24
	}
	return w, h
}

// tick drains the buffer into vt at command boundaries, then repaints.
func (rt *rtState) tick() {
	rt.Lock()
	if !rt.dirty && len(rt.buffer) == 0 && !rt.drain {
		rt.Unlock()
		return
	}

	buf := rt.buffer
	rt.buffer = nil
	if len(buf) > 0 {
		rt.compose(buf)
		rt.dirty = true
	}

	// EOF: the final unfinished command becomes the last block.
	if rt.drain {
		g, _, _ := rt.snapshot()
		if g != nil {
			rt.his = append(rt.his, block{cells: g, width: rt.width, code: rt.finalCode})
			rt.log.event("block " + itoa(len(rt.his)) + " (final)")
			rt.dirty = true
		}
		rt.vt = vt10x.New(vt10x.WithSize(rt.width, rt.height))
	}

	if rt.dirty {
		rt.dirty = false
		rt.Unlock()
		rt.repaint()
		return
	}
	rt.Unlock()
}
