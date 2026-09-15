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

	se := getShellEnv(shell)
	defer se.Cleanup()

	rows, cols, err := pty.Getsize(os.Stdin)
	if err != nil {
		rows, cols = 24, 80
	}

	master, childCmd, err := spawnPTY(shell, Winsize{Rows: uint16(rows), Cols: uint16(cols)}, se.Env, se.Args)
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

	store := NewBlockStore()
	cmdQueue := NewCommandQueue()
	parser := newBlockParser(store, cmdQueue)
	renderCh := make(chan struct{}, 10)

	// PTY reader: feed parser only. No live passthrough to screen.
	go func() {
		buf := make([]byte, 32768)
		prevBlocks := 0
		for {
			n, rerr := master.Read(buf)
			if n > 0 {
				parser.Feed(buf[:n])
				if store.Len() > prevBlocks {
					prevBlocks = store.Len()
					select {
					case renderCh <- struct{}{}:
					default:
					}
				}
			}
			if rerr != nil {
				close(renderCh)
				return
			}
		}
	}()

	// stdin reader: forward keystrokes to PTY
	keyCh := make(chan []byte, 32)
	stdinDone := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := os.Stdin.Read(buf)
			if n > 0 {
				keyCh <- append([]byte(nil), buf[:n]...)
			}
			if rerr != nil {
				close(stdinDone)
				close(keyCh)
				return
			}
		}
	}()

	input := ""
	inEsc := 0
	Render(store, input)

loop:
	for {
		select {
		case _, ok := <-renderCh:
			if !ok {
				break loop
			}
			Render(store, input)

		case <-sigwinch:
			rows, cols, _ := pty.Getsize(os.Stdin)
			_ = resizePTY(master, uint16(rows), uint16(cols))
			Render(store, input)

		case <-sigint:
			if childCmd != nil && childCmd.Process != nil {
				_ = syscall.Kill(-childCmd.Process.Pid, syscall.SIGINT)
			}

		case <-sigterm:
			_ = master.Close()
			break loop

		case <-stdinDone:
			break loop

		case data, ok := <-keyCh:
			if !ok {
				break loop
			}
			changed, newInput := handleInput(data, master, cmdQueue, input, &inEsc)
			if changed {
				input = newInput
				Render(store, input)
			}
		}
	}

	_ = childCmd.Wait()
	code := 0
	if childCmd.ProcessState != nil {
		code = childCmd.ProcessState.ExitCode()
	}
	os.Exit(code)
}

// handleInput forwards key bytes to the child and tracks the prompt line
// for display. Returns (displayChanged, newInput).
func handleInput(data []byte, master *os.File, cmds *CommandQueue, input string, inEsc *int) (bool, string) {
	changed := false
	for _, b := range data {
		if b == 0x1b {
			*inEsc = 3
		}
		if *inEsc > 0 {
			*inEsc--
			_, _ = master.Write([]byte{b})
			continue
		}

		switch b {
		case '\r':
			cmds.Push(input)
			_, _ = master.Write([]byte{b})
			input = ""
			changed = true
		case '\n':
			_, _ = master.Write([]byte{b})
		case 0x7f, 0x08:
			_, _ = master.Write([]byte{b})
			if len(input) > 0 {
				input = input[:len(input)-1]
				changed = true
			}
		case 0x03, 0x04, 0x15:
			_, _ = master.Write([]byte{b})
			input = ""
			changed = true
		default:
			_, _ = master.Write([]byte{b})
			if b >= 0x20 {
				input += string(b)
				changed = true
			}
		}
	}
	return changed, input
}