package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

func TestDelimiterMarkers(t *testing.T) {
	se := getShellEnv("/bin/zsh")
	defer se.Cleanup()

	master, _, err := spawnPTY("/bin/zsh", Winsize{Rows: 40, Cols: 120}, se.Env, se.Args)
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