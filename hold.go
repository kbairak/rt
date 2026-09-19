package main

import (
	"bytes"
	"io"
	"iter"
)

// holdIter wraps a source reader and yields chunks while guaranteeing that no
// watched token (as decided by hold) is split across two yields: a trailing
// portion of each combined chunk that is a proper prefix of a watched sequence
// is withheld until the next read disambiguates it. The final error (io.EOF
// included) is yielded once, after any withheld bytes have been flushed.
func holdIter(src io.Reader, hold func([]byte) int) iter.Seq2[[]byte, error] {
	return func(yield func([]byte, error) bool) {
		tmp := make([]byte, 32768)
		var pending []byte
		for {
			n, err := src.Read(tmp)
			if n > 0 {
				comb := make([]byte, 0, len(pending)+n)
				comb = append(comb, pending...)
				comb = append(comb, tmp[:n]...)
				pending = nil
				h := hold(comb)
				if h > 0 {
					pending = append([]byte(nil), comb[len(comb)-h:]...)
					comb = comb[:len(comb)-h]
				}
				if len(comb) > 0 {
					if !yield(comb, nil) {
						return
					}
				}
			}
			if err != nil {
				if len(pending) > 0 {
					if !yield(pending, nil) {
						return
					}
					pending = nil
				}
				yield(nil, err)
				return
			}
		}
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
