package main

import (
	"io"
	"testing"
	"time"

	"github.com/creack/pty"
)

// full-pipeline: real zsh under a PTY, keystrokes routed through
// forwardInput + parser fed from the PTY reader. Asserts blocks form with
// command + output preserved.
func TestPipelineFormsBlocks(t *testing.T) {
	env, args, cleanup, err := getShellEnv("/bin/zsh")
	if err != nil {
		t.Fatalf("getShellEnv: %v", err)
	}
	defer cleanup()

	master, _, err := spawnPTY("/bin/zsh", pty.Winsize{Rows: 40, Cols: 120}, env, args)
	if err != nil {
		t.Fatalf("spawnPTY: %v", err)
	}
	defer master.Close()

	store := &BlockStore{}
	p := newBlockParser(store, 120)

	done := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := master.Read(buf)
			if n > 0 {
				p.Feed(buf[:n])
			}
			if rerr != nil {
				close(done)
				return
			}
		}
	}()

	time.Sleep(500 * time.Millisecond)
	forwardInput([]byte("echo hello\r"), master)
	_, _ = io.WriteString(master, "exit\r")

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for shell to exit")
	}

	blocks := store.All()
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if !gridContains(blocks[0].Cells, "echo hello") {
		t.Fatalf("block 0 should carry the echoed command line: %v", blocks[0].Cells)
	}
	if !gridContains(blocks[0].Cells, "hello") {
		t.Fatalf("block 0 missing output 'hello': %v", blocks[0].Cells)
	}
}
