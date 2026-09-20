package main

import (
	"bytes"
	"sync"
	"testing"

	"github.com/hinshun/vt10x"
)

func replyVT(buf *bytes.Buffer) vt10x.Terminal {
	return vt10x.New(
		vt10x.WithSize(20, 5),
		vt10x.WithWriter(vtReplyWriter{w: buf, mu: &sync.Mutex{}}),
	)
}

func TestVTReplyWriterAnswersCPR(t *testing.T) {
	var buf bytes.Buffer
	vt := replyVT(&buf)

	if _, err := vt.Write([]byte("\x1b[6n")); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "\x1b[1;1R" {
		t.Fatalf("initial CPR=%q want \\x1b[1;1R", got)
	}

	buf.Reset()
	if _, err := vt.Write([]byte("\x1b[3;4H\x1b[6n")); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "\x1b[3;4R" {
		t.Fatalf("moved CPR=%q want \\x1b[3;4R", got)
	}
}

func TestVTReplyWriterAnswersDSR(t *testing.T) {
	var buf bytes.Buffer
	vt := replyVT(&buf)

	if _, err := vt.Write([]byte("\x1b[5n")); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "\x1b[0n" {
		t.Fatalf("DSR=%q want \\x1b[0n", got)
	}
}
