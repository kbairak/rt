package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestDelimiterMarkers(t *testing.T) {
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

	var buf bytes.Buffer
	done := make(chan error, 1)

	go func() {
		tmp := make([]byte, 4096)
		for {
			n, rerr := master.Read(tmp)
			if n > 0 {
				buf.Write(tmp[:n])
			}
			if rerr != nil {
				done <- rerr
				return
			}
		}
	}()

	time.Sleep(500 * time.Millisecond)
	_, _ = io.WriteString(master, "echo hello\nexit\n")

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for output")
	}

	master.Close()

	out := buf.String()
	if !strings.Contains(out, "RTMRK") {
		t.Fatalf("missing RT marker in output:\n%q", out)
	}
	if !strings.Contains(out, "echo hello") {
		t.Fatalf("missing command 'echo hello' in output:\n%q", out)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("missing 'hello' echo output:\n%q", out)
	}
}
