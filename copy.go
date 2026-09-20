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
// is an index into history (0 = oldest); scroll is the first history line shown
// (0 = the newest entry's separator).
type overlay struct {
	sel    int
	scroll int
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
			if s.sel > 0 {
				s.sel--
			}
		case ovNewer:
			if s.sel < len(s.history)-1 {
				s.sel++
			}
		case ovOldest:
			s.sel = 0
		case ovNewest:
			if len(s.history) > 0 {
				s.sel = len(s.history) - 1
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
	s.scroll = adjustScroll(s.history, s.sel, s.scroll, viewHeight(s.height))
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

// entryHeight is the number of screen lines a history entry occupies: its
// separator plus one line per grid row.
func entryHeight(b block) int {
	return 1 + len(b.cells)
}

// totalLines is the total number of history lines in newest-first order.
func totalLines(his []block) int {
	n := 0
	for _, b := range his {
		n += entryHeight(b)
	}
	return n
}

// topLine returns the line index of entry i's separator. Line 0 is the newest
// entry's separator; older entries follow downward.
func topLine(his []block, i int) int {
	t := 0
	for j := len(his) - 1; j > i; j-- {
		t += entryHeight(his[j])
	}
	return t
}

// viewHeight is the number of history rows visible in copy mode: the terminal
// height minus the status row.
func viewHeight(height int) int {
	if v := height - 1; v > 0 {
		return v
	}
	return 1
}

// adjustScroll moves scroll as little as possible so entry sel is fully
// visible. A block taller than the viewport is aligned to the top instead.
func adjustScroll(his []block, sel, scroll, v int) int {
	if len(his) == 0 {
		return 0
	}
	if sel < 0 {
		sel = 0
	}
	if sel >= len(his) {
		sel = len(his) - 1
	}
	t := topLine(his, sel)
	h := entryHeight(his[sel])
	if h > v {
		scroll = t
	} else {
		lo := t + h - v // bottom aligned: reveal the last line
		if lo < 0 {
			lo = 0
		}
		switch {
		case scroll < lo:
			scroll = lo
		case scroll > t:
			scroll = t
		}
	}
	max := totalLines(his) - v
	if max < 0 {
		max = 0
	}
	if scroll < 0 {
		scroll = 0
	} else if scroll > max {
		scroll = max
	}
	return scroll
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
