package main

import "strings"

// copyKey is the byte that opens the copy overlay at the idle prompt: CTRL-^
// (0x1e). It is unbound in readline emacs mode, zsh vi mode, bash, Python
// readline, vim and tmux defaults.
const copyKey = 0x1e

// gutterLine is the green vertical bar drawn in copy mode's left gutter beside
// the selected block.
const gutterLine = "│"

// overlay is the copy-mode view state handed to the renderer while active. sel
// is an index into history (0 = oldest), always the representative block of a
// collapsed duplicate run.
type overlay struct {
	sel int
}

// overlayAction is a decoded keystroke in copy mode.
type overlayAction int

const (
	ovNone overlayAction = iota
	ovOlder
	ovNewer
	ovOldest
	ovNewest
	ovCopy
	ovCancel
)

// decodeOverlay parses raw stdin bytes into copy-mode actions, in order. A
// trailing incomplete escape sequence is returned as pending for the next read.
func decodeOverlay(pend, data []byte) (acts []overlayAction, pending []byte) {
	buf := data
	if len(pend) > 0 {
		buf = append(append([]byte(nil), pend...), data...)
	}
	for i := 0; i < len(buf); {
		b := buf[i]
		if b == 0x1b {
			// A lone trailing ESC is the Esc key, not a split sequence:
			// terminals write multi-byte sequences atomically.
			if i+1 >= len(buf) {
				acts = append(acts, ovCancel)
				i++
				continue
			}
			switch buf[i+1] {
			case '[':
				if i+2 >= len(buf) {
					return acts, append([]byte(nil), buf[i:]...)
				}
				switch buf[i+2] {
				case 'A':
					acts = append(acts, ovNewer)
					i += 3
				case 'B':
					acts = append(acts, ovOlder)
					i += 3
				case 'H':
					acts = append(acts, ovNewest)
					i += 3
				case 'F':
					acts = append(acts, ovOldest)
					i += 3
				case '1', '4':
					if i+3 >= len(buf) {
						return acts, append([]byte(nil), buf[i:]...)
					}
					if buf[i+3] == '~' {
						if buf[i+2] == '1' {
							acts = append(acts, ovNewest)
						} else {
							acts = append(acts, ovOldest)
						}
						i += 4
					} else {
						i += 3
					}
				default:
					i += 3
				}
			case 'O':
				if i+2 >= len(buf) {
					return acts, append([]byte(nil), buf[i:]...)
				}
				switch buf[i+2] {
				case 'H':
					acts = append(acts, ovNewest)
				case 'F':
					acts = append(acts, ovOldest)
				}
				i += 3
			default:
				acts = append(acts, ovCancel)
				i++
			}
			continue
		}
		switch b {
		case 'j', 0x0e:
			acts = append(acts, ovOlder)
		case 'k', 0x10:
			acts = append(acts, ovNewer)
		case 'g':
			acts = append(acts, ovNewest)
		case 'G':
			acts = append(acts, ovOldest)
		case '\r', '\n', 'y':
			acts = append(acts, ovCopy)
		case 'q', 0x07, 0x03, copyKey:
			acts = append(acts, ovCancel)
		}
		i++
	}
	return acts, nil
}

// handleCopyInput applies decoded overlay actions. A copy or cancel closes the
// overlay; the caller's loop then repaints (and, for copy, flushes the write).
// Caller must not hold mu.
func (s *session) handleCopyInput(data []byte) {
	acts, pend := decodeOverlay(s.copyPend, data)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.copyPend = pend

	for _, a := range acts {
		switch a {
		case ovOlder:
			s.sel = s.repAt(s.olderThan(s.sel))
		case ovNewer:
			if s.sel < len(s.history)-1 {
				s.sel = s.repAt(s.sel + 1)
			}
		case ovOldest:
			if len(s.history) > 0 {
				s.sel = s.repAt(0)
			}
		case ovNewest:
			if len(s.history) > 0 {
				s.sel = s.repAt(len(s.history) - 1)
			}
		case ovCopy:
			s.copyPending = true
			s.copySel = s.sel
			s.closeOverlay()
			return
		case ovCancel:
			s.closeOverlay()
			return
		}
	}
	s.dirty = true
	s.wake()
}

// closeOverlay exits copy mode and asks for a repaint. Caller holds mu.
func (s *session) closeOverlay() {
	s.copyActive = false
	s.copyPend = nil
	s.dirty = true
	s.wake()
}

// repAt returns the newest index of the duplicate run containing i, i.e. the
// block the renderer actually draws for that run.
func (s *session) repAt(i int) int {
	if i < 0 || i >= len(s.history) {
		return i
	}
	for i+1 < len(s.history) && blocksEqual(s.history[i], s.history[i+1]) {
		i++
	}
	return i
}

// olderThan returns the index of the nearest older block that starts a distinct
// run, so a collapsed duplicate run is crossed in one step.
func (s *session) olderThan(i int) int {
	if i <= 0 || len(s.history) == 0 {
		return 0
	}
	j := i - 1
	for j > 0 && blocksEqual(s.history[j], s.history[i]) {
		j--
	}
	return j
}

// overlayStatus renders the bottom status row for copy mode: the selected block
// counted from newest and the key legend, reverse-video padded to the full
// width.
func overlayStatus(ov *overlay, total, width int) string {
	text := "COPY"
	if total > 0 {
		text = "COPY " + itoa(total-ov.sel) + "/" + itoa(total)
	}
	text += "   ⏎/y copy   j/k move   esc cancel"
	return reverseLine(text, width)
}

// reverseLine reverse-videos text, truncating or space-padding it to width.
func reverseLine(text string, width int) string {
	if width < 1 {
		return ""
	}
	r := []rune(text)
	if len(r) > width {
		r = r[:width]
	}
	s := string(r)
	if pad := width - len(r); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return ansiReverse + s + ansiReset
}
