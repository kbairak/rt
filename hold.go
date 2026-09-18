package main

import (
	"bytes"
	"io"
)

// holdReader wraps a source reader and withholds the trailing portion of each
// combined chunk that is a proper prefix of one of the watched sequences, so
// no watched token is ever delivered split across two Read calls.
type holdReader struct {
	src     io.Reader
	hold    func([]byte) int
	pending []byte // withheld tail awaiting disambiguation
	out     []byte // deliverable that didn't fit the last p
	tmp     []byte // per-call src read scratch
	err     error  // src error, surfaced after pending/out flushed
}

func newHoldReader(src io.Reader, hold func([]byte) int) *holdReader {
	return &holdReader{src: src, hold: hold, tmp: make([]byte, 4096)}
}

// Read delivers from the source while guaranteeing watched sequences arrive
// whole. Never returns (0, nil) for a non-empty p (would busy-loop).
func (r *holdReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if len(r.out) > 0 {
			n := copy(p, r.out)
			r.out = r.out[n:]
			return n, nil
		}
		if r.err != nil {
			if len(r.pending) > 0 {
				n := copy(p, r.pending)
				if n == len(r.pending) {
					r.pending = nil
				} else {
					r.pending = r.pending[n:]
				}
				return n, nil
			}
			return 0, r.err
		}
		n, err := r.src.Read(r.tmp)
		var srcChunk []byte
		if n > 0 {
			srcChunk = r.tmp[:n]
		}
		if err != nil {
			r.err = err
		}
		if len(srcChunk) == 0 {
			if r.err != nil && len(r.pending) == 0 {
				return 0, r.err
			}
			continue
		}
		comb := make([]byte, 0, len(r.pending)+len(srcChunk))
		comb = append(comb, r.pending...)
		comb = append(comb, srcChunk...)
		r.pending = nil
		h := r.hold(comb)
		var del []byte
		if h > 0 {
			r.pending = append([]byte(nil), comb[len(comb)-h:]...)
			del = comb[:len(comb)-h]
		} else {
			del = comb
		}
		if len(del) == 0 {
			continue
		}
		n = copy(p, del)
		if n < len(del) {
			r.out = append([]byte(nil), del[n:]...)
		}
		return n, nil
	}
}

// holdPrefixes returns the length of the longest suffix of b that is a proper
// prefix of any of pfx. 0 if none.
func holdPrefixes(b []byte, pfx [][]byte) int {
	best := 0
	for _, p := range pfx {
		limit := len(p) - 1
		if limit > len(b) {
			limit = len(b)
		}
		for n := limit; n > 0; n-- {
			if bytes.HasSuffix(b, p[:n]) {
				if n > best {
					best = n
				}
				break
			}
		}
	}
	return best
}