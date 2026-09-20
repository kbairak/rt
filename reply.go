package main

import (
	"io"
	"sync"

	"github.com/hinshun/vt10x"
)

// vtReplyWriter forwards terminal responses the emulator generates (CPR from
// ESC[6n, DSR from ESC[5n) to the pty. Applications such as fzf query the
// cursor position and block until they get a reply; vt10x writes those replies
// to its writer, which defaults to io.Discard.
//
// Writes go to the pty (never rt's stdout) and are serialized with forward()
// via the session's ptyMu. Write must not take the model lock: it runs inside
// vt10x.Write, which compose calls while holding it.
type vtReplyWriter struct {
	w   io.Writer
	mu  *sync.Mutex
	log *recorder
}

func (r vtReplyWriter) Write(p []byte) (int, error) {
	if r.log != nil {
		r.log.event("reply " + ansiRepr(p))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.w.Write(p)
}

// newVT builds an emulator whose query replies are routed to the pty. Caller
// holds s.mu.
func (s *session) newVT() vt10x.Terminal {
	return vt10x.New(
		vt10x.WithSize(s.width, s.height),
		vt10x.WithWriter(vtReplyWriter{w: s.master, mu: &s.ptyMu, log: s.log}),
	)
}
