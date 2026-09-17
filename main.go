package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/creack/pty"
)

func main() {
	shell := "/bin/zsh"
	if len(os.Args) > 1 {
		shell = os.Args[1]
	}

	restore, err := InitTerminal()
	if err != nil {
		fmt.Fprintf(os.Stderr, "init terminal: %v\n", err)
		os.Exit(1)
	}
	defer restore()

	env, args, cleanup, err := getShellEnv(shell)
	if err != nil {
		fmt.Fprintf(os.Stderr, "shell env: %v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	rows, cols, err := pty.Getsize(os.Stdin)
	if err != nil || rows < 1 || cols < 1 {
		rows, cols = 24, 80
	}

	master, childCmd, err := spawnPTY(shell, pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)}, env, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spawn pty: %v\n", err)
		os.Exit(1)
	}
	defer master.Close()

	sigwinch := make(chan os.Signal, 1)
	signal.Notify(sigwinch, syscall.SIGWINCH)

	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, syscall.SIGINT)

	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGTERM)

	store := &BlockStore{}
	parser := newBlockParser(store, cols)
	parser.Resize(rows, cols)
	renderCh := make(chan struct{}, 10)

	// PTY reader: feed the emulator + parser, signal for repaint.
	go func() {
		buf := make([]byte, 32768)
		for {
			n, rerr := master.Read(buf)
			if n > 0 {
				parser.Feed(buf[:n])
				select {
				case renderCh <- struct{}{}:
				default:
				}
			}
			if rerr != nil {
				close(renderCh)
				return
			}
		}
	}()

	// stdin reader: forward keystrokes to the PTY.
	stdinDone := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := os.Stdin.Read(buf)
			if n > 0 {
				forwardInput(buf[:n], master)
			}
			if rerr != nil {
				close(stdinDone)
				return
			}
		}
	}()

	Render(store, parser.Live())

loop:
	for {
		select {
		case _, ok := <-renderCh:
			if !ok {
				break loop
			}
			// coalesce bursts: drain all pending repaint signals
			for {
				select {
				case <-renderCh:
				default:
					goto done
				}
			}
		done:
			Render(store, parser.Live())

		case <-sigwinch:
			rows, cols, _ = pty.Getsize(os.Stdin)
			if rows < 1 || cols < 1 {
				rows, cols = 24, 80
			}
			_ = resizePTY(master, uint16(rows), uint16(cols))
			parser.Resize(rows, cols)
			Render(store, parser.Live())

		case <-sigint:
			if childCmd != nil && childCmd.Process != nil {
				_ = syscall.Kill(-childCmd.Process.Pid, syscall.SIGINT)
			}

		case <-sigterm:
			_ = master.Close()
			break loop

		case <-stdinDone:
			break loop
		}
	}

	_ = childCmd.Wait()
	code := 0
	if childCmd.ProcessState != nil {
		code = childCmd.ProcessState.ExitCode()
	}
	os.Exit(code)
}

// forwardInput relays keystrokes straight to the child. The command text
// is not tracked here — the archive grid's own echo row is the canonical
// record of what was typed.
func forwardInput(data []byte, master *os.File) {
	_, _ = master.Write(data)
}
