package main

import (
	"errors"
	"io"
	"testing"
)

// chunkReader serves its chunks in order, then io.EOF.
type chunkReader struct {
	chunks [][]byte
	last   []byte
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(c.chunks) == 0 {
		return 0, io.EOF
	}
	ch := c.chunks[0]
	c.chunks = c.chunks[1:]
	n := copy(p, ch)
	c.last = append(c.last[:0], p[:n]...)
	return n, nil
}

// drain runs a holdIter to completion, collecting every non-empty chunk. It
// fails on any non-EOF error.
func drain(t *testing.T, src io.Reader, hold func([]byte) int) [][]byte {
	t.Helper()
	var got [][]byte
	for chunk, err := range holdIter(src, hold) {
		if len(chunk) > 0 {
			got = append(got, append([]byte(nil), chunk...))
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatalf("unexpected error: %v", err)
			}
			return got
		}
	}
	return got
}

func TestUserContract(t *testing.T) {
	cases := []struct {
		name   string
		chunks []string
		want   []string
	}{
		{"single chunk, no token", []string{"hello"}, []string{"hello"}},
		{"token complete in one chunk", []string{"hello SEPARATOR world"}, []string{"hello SEPARATOR world"}},
		{"token split at two chunks", []string{"hello SEPARAT", "OR world"}, []string{"hello ", "SEPARATOR world"}},
		{"token split at three chunks", []string{"hello SEP", "ARAT", "OR world"}, []string{"hello ", "SEPARATOR world"}},
		{"partial then divergent", []string{"hello SEP", "arat", "OR world"}, []string{"hello ", "SEParat", "OR world"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			chunks := make([][]byte, len(c.chunks))
			for i, s := range c.chunks {
				chunks[i] = []byte(s)
			}
			got := drain(t, &chunkReader{chunks: chunks}, func(b []byte) int {
				return holdPrefixes(b, [][]byte{[]byte("SEPARATOR")})
			})
			if len(got) != len(c.want) {
				t.Fatalf("got %d reads %q, want %d %q", len(got), got, len(c.want), c.want)
			}
			for i := range got {
				if string(got[i]) != c.want[i] {
					t.Fatalf("read %d = %q, want %q\nall: got %q want %q", i, got[i], c.want[i], got, c.want)
				}
			}
		})
	}
}

func TestPurePartialHolds(t *testing.T) {
	got := drain(t, &chunkReader{chunks: [][]byte{[]byte("SEP"), []byte("ARATOR rest")}}, func(b []byte) int {
		return holdPrefixes(b, [][]byte{[]byte("SEPARATOR")})
	})
	if len(got) != 1 || string(got[0]) != "SEPARATOR rest" {
		t.Fatalf("got %q, want [\"SEPARATOR rest\"]", got)
	}
}

func TestEOFMidPrefix(t *testing.T) {
	got := drain(t, &chunkReader{chunks: [][]byte{[]byte("hello SEP")}}, func(b []byte) int {
		return holdPrefixes(b, [][]byte{[]byte("SEPARATOR")})
	})
	if len(got) != 2 || string(got[0]) != "hello " || string(got[1]) != "SEP" {
		t.Fatalf("got %q, want [\"hello \" \"SEP\"]", got)
	}
}

func TestMultipleWatchedPrefixes(t *testing.T) {
	watched := [][]byte{ansiSepHead, []byte(ansiEnterAltScreen), []byte(ansiLeaveAltScreen)}
	got := drain(t, &chunkReader{chunks: [][]byte{
		[]byte("hello \x1b[?1049"),
		[]byte("h"),
		[]byte("world "),
	}}, func(b []byte) int {
		return holdPrefixes(b, watched)
	})
	want := []string{"hello ", "\x1b[?1049h", "world "}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range got {
		if string(got[i]) != want[i] {
			t.Fatalf("read %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAtomicEnterAlt(t *testing.T) {
	watched := [][]byte{ansiSepHead, []byte(ansiEnterAltScreen), []byte(ansiLeaveAltScreen)}
	got := drain(t, &chunkReader{chunks: [][]byte{[]byte("\x1b[?1049"), []byte("h")}}, func(b []byte) int {
		return holdPrefixes(b, watched)
	})
	if len(got) != 1 || string(got[0]) != "\x1b[?1049h" {
		t.Fatalf("got %q, want [\"\\x1b[?1049h\"]", got)
	}
}

func TestBothSequencesOrderPreserved(t *testing.T) {
	watched := [][]byte{ansiSepHead, []byte(ansiEnterAltScreen), []byte(ansiLeaveAltScreen)}
	stream := "\x1b[?1049hhello\x1b]RT;7f3a9b;0\x07\x1b[?1049l"
	got := drain(t, &chunkReader{chunks: [][]byte{
		[]byte("\x1b[?1049hhel"),
		[]byte("lo\x1b]RT;7f3a9b;0"),
		[]byte("\x07\x1b[?1049l"),
	}}, func(b []byte) int {
		return holdPrefixes(b, watched)
	})
	joined := ""
	for _, g := range got {
		joined += string(g)
	}
	if joined != stream {
		t.Fatalf("joined %q, want %q", joined, stream)
	}
}
