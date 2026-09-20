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
// is an index into the visible view (0 = oldest); scroll is the first line shown
// (0 = the newest entry's separator). searching/query drive the filter input
// line; filter is the applied query (empty = none).
type overlay struct {
	sel       int
	scroll    int
	searching bool
	query     string
	filter    string
}

// overlayAction is a decoded keystroke in copy mode.
type overlayAction int

const (
	ovNone overlayAction = iota
	ovOlder
	ovNewer
	ovOldest
	ovNewest
	ovPageDown
	ovPageUp
	ovSearch
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
		case 0x04:
			acts = append(acts, ovPageDown)
		case 0x15:
			acts = append(acts, ovPageUp)
		case '/':
			acts = append(acts, ovSearch)
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
	s.mu.Lock()
	if s.searchActive {
		s.mu.Unlock()
		s.handleSearchInput(data)
		return
	}
	s.mu.Unlock()

	acts, pend := decodeOverlay(s.copyPend, data)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.copyPend = pend

	adjust := false
	for _, a := range acts {
		switch a {
		case ovOlder:
			if s.sel > 0 {
				s.sel--
			}
			adjust = true
		case ovNewer:
			if s.sel < len(s.view)-1 {
				s.sel++
			}
			adjust = true
		case ovOldest:
			s.sel = 0
			adjust = true
		case ovNewest:
			if len(s.view) > 0 {
				s.sel = len(s.view) - 1
			}
			adjust = true
		case ovPageDown:
			s.page(1)
		case ovPageUp:
			s.page(-1)
		case ovSearch:
			s.searchActive = true
			s.query = ""
			s.searchPend = nil
			s.dirty = true
			s.wake()
			return
		case ovCopy:
			s.copyPending = true
			s.copySel = s.sel
			s.copyText = ""
			if s.sel >= 0 && s.sel < len(s.view) {
				s.copyText = blockText(s.view[s.sel].cells)
			}
			s.closeOverlay()
			return
		case ovCancel:
			s.closeOverlay()
			return
		}
	}
	if adjust {
		s.scroll = adjustScroll(s.view, s.sel, s.scroll, viewHeight(s.height))
	}
	s.dirty = true
	s.wake()
}

// page scrolls the viewport by half a page and re-anchors the selection. The
// selection only changes when the previously selected block is no longer fully
// visible: it becomes the top block whose first line is in the new window, or,
// when the window sits entirely inside one block, that block.
func (s *session) page(delta int) {
	v := viewHeight(s.height)
	step := v / 2
	if step < 1 {
		step = 1
	}
	max := totalLines(s.view) - v
	if max < 0 {
		max = 0
	}
	ns := s.scroll + delta*step
	if ns < 0 {
		ns = 0
	} else if ns > max {
		ns = max
	}
	s.scroll = ns

	if len(s.view) == 0 || blockFullyVisible(s.view, s.sel, ns, v) {
		return
	}
	if k, ok := blockStartingAt(s.view, ns, v); ok {
		s.sel = k
		return
	}
	s.sel = blockAtLine(s.view, ns)
}

// searchEventKind classifies a decoded search-mode keystroke.
type searchEventKind int

const (
	seRune searchEventKind = iota
	seBackspace
	seApply
	seCancel
)

type searchEvent struct {
	kind searchEventKind
	r    rune
}

// decodeSearch parses raw stdin into search-mode events. Only ASCII printable
// bytes become query runes; control bytes and escape sequences are ignored
// (except Enter, Backspace and Esc). A trailing incomplete escape sequence is
// returned as pending for the next read.
func decodeSearch(pend, data []byte) (evs []searchEvent, pending []byte) {
	buf := data
	if len(pend) > 0 {
		buf = append(append([]byte(nil), pend...), data...)
	}
	for i := 0; i < len(buf); {
		b := buf[i]
		if b == 0x1b {
			if i+1 >= len(buf) {
				evs = append(evs, searchEvent{kind: seCancel})
				i++
				continue
			}
			switch buf[i+1] {
			case '[':
				if i+2 >= len(buf) {
					return evs, append([]byte(nil), buf[i:]...)
				}
				j := i + 2
				for j < len(buf) && !(buf[j] >= 0x40 && buf[j] <= 0x7e) {
					j++
				}
				if j >= len(buf) {
					return evs, append([]byte(nil), buf[i:]...)
				}
				i = j + 1
			case 'O':
				if i+2 >= len(buf) {
					return evs, append([]byte(nil), buf[i:]...)
				}
				i += 3
			default:
				evs = append(evs, searchEvent{kind: seCancel})
				i++
			}
			continue
		}
		switch {
		case b == 0x0d || b == 0x0a:
			evs = append(evs, searchEvent{kind: seApply})
		case b == 0x7f || b == 0x08:
			evs = append(evs, searchEvent{kind: seBackspace})
		case b >= 0x20 && b <= 0x7e:
			evs = append(evs, searchEvent{kind: seRune, r: rune(b)})
		}
		i++
	}
	return evs, nil
}

// handleSearchInput edits the query, applies it on Enter, or clears the filter
// on Esc. Enter is ignored when nothing matches so the user can keep editing.
func (s *session) handleSearchInput(data []byte) {
	evs, pend := decodeSearch(s.searchPend, data)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.searchPend = pend

	changed := false
	for _, e := range evs {
		switch e.kind {
		case seRune:
			s.query += string(e.r)
			changed = true
		case seBackspace:
			if r := []rune(s.query); len(r) > 0 {
				s.query = string(r[:len(r)-1])
				changed = true
			}
		case seApply:
			matches := filterHistory(s.history, s.query)
			if len(matches) == 0 {
				continue
			}
			s.filter = s.query
			s.view = matches
			s.sel = len(s.view) - 1
			s.scroll = 0
			s.searchActive = false
			s.query = ""
			s.searchPend = nil
			s.dirty = true
			s.wake()
			return
		case seCancel:
			s.filter = ""
			s.view = s.history
			s.sel = len(s.view) - 1
			s.scroll = 0
			s.searchActive = false
			s.query = ""
			s.searchPend = nil
			s.dirty = true
			s.wake()
			return
		}
	}
	if changed {
		s.dirty = true
		s.wake()
	}
}

// filterHistory returns the entries whose plain text contains q (case
// sensitive, exact substring). An empty query returns the input unchanged.
func filterHistory(his []block, q string) []block {
	if q == "" {
		return his
	}
	out := make([]block, 0, len(his))
	for _, b := range his {
		if strings.Contains(blockText(b.cells), q) {
			out = append(out, b)
		}
	}
	return out
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

// blockFullyVisible reports whether entry i (separator through last row) lies
// entirely within the window [scroll, scroll+v).
func blockFullyVisible(his []block, i, scroll, v int) bool {
	if i < 0 || i >= len(his) {
		return false
	}
	t := topLine(his, i)
	return t >= scroll && t+entryHeight(his[i]) <= scroll+v
}

// blockStartingAt returns the topmost entry whose first line falls in the
// window [scroll, scroll+v). ok is false when no entry starts in the window.
func blockStartingAt(his []block, scroll, v int) (int, bool) {
	pos := 0
	for k := len(his) - 1; k >= 0; k-- {
		if pos >= scroll {
			return k, pos < scroll+v
		}
		pos += entryHeight(his[k])
	}
	return 0, false
}

// blockAtLine returns the entry containing line, i.e. the one with the greatest
// top line not after it.
func blockAtLine(his []block, line int) int {
	pos := 0
	for k := len(his) - 1; k >= 0; k-- {
		if h := entryHeight(his[k]); line < pos+h {
			return k
		}
		pos += entryHeight(his[k])
	}
	return 0
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

// overlayStatus renders the bottom status row. In search mode it shows the
// query being typed; otherwise it shows the selected block counted from newest,
// an optional filter indicator, and the key legend. Reverse-video padded to the
// full width.
func overlayStatus(ov *overlay, total, width int) string {
	var text string
	if ov.searching {
		text = "SEARCH " + ov.query + "   ⏎ apply   esc clear"
	} else {
		text = "COPY"
		if total > 0 {
			text = "COPY " + itoa(total-ov.sel) + "/" + itoa(total)
		}
		if ov.filter != "" {
			text += "  /" + ov.filter
		}
		text += "   ⏎/y copy   j/k move   ^u/^d page   esc cancel"
	}
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
