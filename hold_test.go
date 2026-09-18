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

// drain reads a holdReader to io.EOF, collecting every successful Read return.
func drain(t *testing.T, r io.Reader, psize int) [][]byte {
	t.Helper()
	var got [][]byte
	if psize == 0 {
		psize = 4096
	}
	p := make([]byte, psize)
	for {
		n, err := r.Read(p)
		if n > 0 {
			got = append(got, append([]byte(nil), p[:n]...))
		}
		if n == 0 && err == nil {
			t.Fatalf("Read returned (0, nil)")
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatalf("unexpected error: %v", err)
			}
			return got
		}
	}
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
			h := newHoldReader(&chunkReader{chunks: chunks}, func(b []byte) int {
				return holdPrefixes(b, [][]byte{[]byte("SEPARATOR")})
			})
			got := drain(t, h, 0)
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
	h := newHoldReader(&chunkReader{chunks: [][]byte{[]byte("SEP"), []byte("ARATOR rest")}}, func(b []byte) int {
		return holdPrefixes(b, [][]byte{[]byte("SEPARATOR")})
	})
	got := drain(t, h, 0)
	if len(got) != 1 || string(got[0]) != "SEPARATOR rest" {
		t.Fatalf("got %q, want [\"SEPARATOR rest\"]", got)
	}
}

func TestEOFMidPrefix(t *testing.T) {
	h := newHoldReader(&chunkReader{chunks: [][]byte{[]byte("hello SEP")}}, func(b []byte) int {
		return holdPrefixes(b, [][]byte{[]byte("SEPARATOR")})
	})
	got := drain(t, h, 0)
	if len(got) != 2 || string(got[0]) != "hello " || string(got[1]) != "SEP" {
		t.Fatalf("got %q, want [\"hello \" \"SEP\"]", got)
	}
}

func TestPSmall(t *testing.T) {
	h := newHoldReader(&chunkReader{chunks: [][]byte{[]byte("hello SEPARATOR world")}}, func(b []byte) int {
		return holdPrefixes(b, [][]byte{[]byte("SEPARATOR")})
	})
	got := drain(t, h, 3)
	joined := ""
	for _, g := range got {
		joined += string(g)
	}
	if joined != "hello SEPARATOR world" {
		t.Fatalf("joined %q, want %q", joined, "hello SEPARATOR world")
	}
	for _, g := range got {
		if len(g) > 3 {
			t.Fatalf("read of %d exceeds p size 3: %q", len(g), g)
		}
	}
}

func TestMultipleWatchedPrefixes(t *testing.T) {
	watched := [][]byte{sepHead, []byte(enterAlt), []byte(leaveAlt)}
	h := newHoldReader(&chunkReader{chunks: [][]byte{
		[]byte("hello \x1b[?1049"),
		[]byte("h"),
		[]byte("world "),
	}}, func(b []byte) int {
		return holdPrefixes(b, watched)
	})
	got := drain(t, h, 0)
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
	watched := [][]byte{sepHead, []byte(enterAlt), []byte(leaveAlt)}
	h := newHoldReader(&chunkReader{chunks: [][]byte{[]byte("\x1b[?1049"), []byte("h")}}, func(b []byte) int {
		return holdPrefixes(b, watched)
	})
	got := drain(t, h, 0)
	if len(got) != 1 || string(got[0]) != "\x1b[?1049h" {
		t.Fatalf("got %q, want [\"\\x1b[?1049h\"]", got)
	}
}

func TestBothSequencesOrderPreserved(t *testing.T) {
	watched := [][]byte{sepHead, []byte(enterAlt), []byte(leaveAlt)}
	stream := "\x1b[?1049hhello\x1b]RT;7f3a9b;0\x07\x1b[?1049l"
	h := newHoldReader(&chunkReader{chunks: [][]byte{
		[]byte("\x1b[?1049hhel"),
		[]byte("lo\x1b]RT;7f3a9b;0"),
		[]byte("\x07\x1b[?1049l"),
	}}, func(b []byte) int {
		return holdPrefixes(b, watched)
	})
	got := drain(t, h, 0)
	joined := ""
	for _, g := range got {
		joined += string(g)
	}
	if joined != stream {
		t.Fatalf("joined %q, want %q", joined, stream)
	}
}