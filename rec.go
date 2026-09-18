package main

import (
	"bufio"
	"fmt"
	"os"
	"sync"
	"time"
	"unicode/utf8"
)

// recorder writes one timestamped, classified line per byte-run/event to a
// per-invocation log file.
type recorder struct {
	mu   sync.Mutex
	init time.Time
	f    *os.File
	w    *bufio.Writer
	txN  int
	rxN  int
}

func newRecorder() (*recorder, error) {
	name := "rt-" + time.Now().Format("20060102-150405") + ".log"
	f, err := os.Create(name)
	if err != nil {
		return nil, err
	}
	return &recorder{init: time.Now(), f: f, w: bufio.NewWriter(f)}, nil
}

func (r *recorder) Close() {
	r.mu.Lock()
	r.w.Flush()
	r.f.Close()
	r.mu.Unlock()
}

// event logs a synthetic lifecycle marker.
func (r *recorder) event(what string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintf(r.w, "%7.1fms --                 EVENT     %s\n", msSince(r.init), what)
}

// tx logs host-stdin bytes forwarded into the pty.
func (r *recorder) tx(b []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	start := r.txN + 1
	r.txN += len(b)
	r.classify("tx", b, start)
}

// rx logs pty output read by rt.
func (r *recorder) rx(b []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	start := r.rxN + 1
	r.rxN += len(b)
	r.classify("rx", b, start)
}

// classify splits data into TEXT / ASCII / ANSI / MULTIBYTE runs and writes
// one log line per run.
func (r *recorder) classify(side string, data []byte, base int) {
	idx := base - 1
	for i := 0; i < len(data); {
		b := data[i]
		switch {
		case b == 0x1b:
			n := ansiSeqLen(data[i:])
			seq := data[i : i+n]
			idx += n
			if r.w.Buffered() > 1<<16 {
				r.w.Flush()
			}
			r.line(side, idx-n+1, idx, "ANSI", ansiRepr(seq)+ansiMeaning(seq))
			i += n
		case b >= 0x80:
			rune, size := utf8.DecodeRune(data[i:])
			idx += size
			r.line(side, idx-size+1, idx, "MULTIBYTE", fmt.Sprintf("%s U+%04X", string(rune), rune))
			i += size
		case b >= 0x21:
			j := i
			for j < len(data) && data[j] >= 0x21 {
				j++
			}
			idx += j - i
			r.line(side, idx-(j-i)+1, idx, "TEXT", string(data[i:j]))
			i = j
		default:
			j := i
			for j < len(data) && data[j] == b {
				j++
			}
			idx += j - i
			name := label(b)
			if j-i > 1 {
				name += fmt.Sprintf(" %dx", j-i)
			}
			r.line(side, idx-(j-i)+1, idx, "ASCII", name)
			i = j
		}
	}
}

func (r *recorder) line(side string, start, end int, typ, payload string) {
	idx := fmt.Sprintf("%d-%d", start, end)
	if start == end {
		idx = fmt.Sprintf("%d", start)
	}
	fmt.Fprintf(r.w, "%7.1fms %-2s %9s %-9s %s\n", msSince(r.init), side, idx, typ, payload)
}

func msSince(t0 time.Time) float64 {
	return float64(time.Since(t0).Microseconds()) / 1000.0
}

func label(b byte) string {
	switch b {
	case '\r':
		return "CR"
	case '\n':
		return "LF"
	case '\x1b':
		return "ESC"
	case '\t':
		return "TAB"
	case ' ':
		return "SPACE"
	default:
		return "CTRL 0x" + fmt.Sprintf("%02x", b)
	}
}
