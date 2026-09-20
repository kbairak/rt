package main

import (
	"fmt"
	"strings"

	"github.com/hinshun/vt10x"
)

// ANSI escape building blocks. Every Go-side ANSI sequence lives here.
const (
	ansiEsc = byte(0x1b)
	ansiBel = byte(0x07)
)

// ansiRtPayload is the distinctive body of the zero-width OSC marker embedded
// in the shell prompt. A user cannot type it, so accidental matches are
// impossible.
const ansiRtPayload = "RT;7f3a9b"

// ansiSepHead opens the zero-width prompt marker; the shell appends the last
// command's exit code (digits) then ansiSepEnd (BEL).
var ansiSepHead = []byte(ansiOscOpen(ansiRtPayload + ";"))

const ansiSepEnd = ansiBel

// DEC 1049 alt-screen toggle, tracked so a ^L at the rt prompt is only
// intercepted when the shell (not a fullscreen app) is reading.
const (
	ansiAltPrefix      = "\x1b[?1049"
	ansiEnterAltScreen = ansiAltPrefix + "h"
	ansiLeaveAltScreen = ansiAltPrefix + "l"
)

// Screen control sequences.
const (
	ansiHideCursor  = "\x1b[?25l"
	ansiShowCursor  = "\x1b[?25h"
	ansiReset       = "\x1b[0m"
	ansiClearScreen = "\x1b[H\x1b[2J"
	ansiClearLine   = "\x1b[K"
)

// Synchronized update mode (DEC 2026): the terminal defers rendering until the
// closing sequence. Terminals that do not support it ignore both harmlessly.
const (
	ansiSyncBegin = "\x1b[?2026h"
	ansiSyncEnd   = "\x1b[?2026l"
)

// ansiOsc wraps a payload in an OSC sequence (ESC ] payload BEL).
func ansiOsc(payload string) string {
	return "\x1b]" + payload + "\x07"
}

// ansiOscOpen starts an OSC sequence without its terminator.
func ansiOscOpen(payload string) string {
	return "\x1b]" + payload
}

// ansiCsiSeq builds a CSI sequence (ESC [ params final).
func ansiCsiSeq(params string, final byte) string {
	return "\x1b[" + params + string(final)
}

// ansiCursorPos moves the cursor to a 0-based row/col (1-based ANSI).
func ansiCursorPos(row, col int) string {
	return ansiCsiSeq(itoa(row+1)+";"+itoa(col+1), 'H')
}

// ansiSeqLen returns the length of the escape sequence starting at data[0].
func ansiSeqLen(data []byte) int {
	if len(data) < 2 {
		return len(data)
	}
	if data[1] == '[' {
		for i := 2; i < len(data); i++ {
			if !(data[i] >= 0x20 && data[i] <= 0x3f) {
				return i + 1
			}
		}
		return len(data)
	}
	if data[1] == ']' {
		for i := 2; i < len(data); i++ {
			if data[i] == ansiBel {
				return i + 1
			}
		}
		return len(data)
	}
	return 2
}

func ansiRepr(seq []byte) string {
	var b strings.Builder
	for _, c := range seq {
		if c == ansiEsc {
			b.WriteString("ESC")
		} else if c >= 0x20 && c < 0x7f {
			b.WriteRune(rune(c))
		} else {
			b.WriteString("‹" + fmt.Sprintf("%02x", c) + "›")
		}
	}
	return b.String()
}

func ansiMeaning(seq []byte) string {
	if len(seq) < 2 || seq[0] != ansiEsc {
		return ""
	}
	if seq[1] == ']' {
		if seq[len(seq)-1] != ansiBel {
			return " (OSC partial)"
		}
		return " (OSC)"
	}
	if seq[1] != '[' {
		return " (ESC single)"
	}
	if len(seq) < 3 || seq[len(seq)-1] >= 0x20 && seq[len(seq)-1] <= 0x3f {
		return " (CSI partial)"
	}
	params, final := ansiCsiParams(seq)
	if len(params) > 0 && strings.HasPrefix(params[0], "?") {
		params[0] = params[0][1:]
		return ansiPrivateMode(params, final)
	}
	switch final {
	case 'm':
		return ansiSgrName(params)
	case 'A':
		return " (cursor up " + itoa(ansiFirstParam(params, 1)) + ")"
	case 'B':
		return " (cursor down " + itoa(ansiFirstParam(params, 1)) + ")"
	case 'C':
		return " (cursor right " + itoa(ansiFirstParam(params, 1)) + ")"
	case 'D':
		return " (cursor left " + itoa(ansiFirstParam(params, 1)) + ")"
	case 'H':
		return " (cursor home)"
	case 'J':
		return " (erase display)"
	case 'K':
		return " (erase line)"
	case 'r':
		return " (set scroll region)"
	default:
		return ""
	}
}

func ansiFirstParam(params []string, def int) int {
	if len(params) < 1 || params[0] == "" {
		return def
	}
	n, ok := parseInt(params[0])
	if !ok {
		return def
	}
	return n
}

func ansiCsiParams(seq []byte) ([]string, byte) {
	final := seq[len(seq)-1]
	body := seq[2 : len(seq)-1]
	var out []string
	var cur strings.Builder
	for _, c := range body {
		if c == ';' {
			out = append(out, cur.String())
			cur.Reset()
		} else {
			cur.WriteRune(rune(c))
		}
	}
	out = append(out, cur.String())
	return out, final
}

func ansiSgrName(params []string) string {
	var parts []string
	for i := 0; i < len(params); i++ {
		n, ok := parseInt(params[i])
		if !ok {
			continue
		}
		if (n == 38 || n == 48) && i+2 < len(params) {
			p2, ok2 := parseInt(params[i+1])
			if ok2 && p2 == 5 {
				ci, ok3 := parseInt(params[i+2])
				if ok3 {
					var what string
					if n == 38 {
						what = "fg256="
					} else {
						what = "bg256="
					}
					parts = append(parts, what+itoa(ci))
					i += 2
					continue
				}
			}
		}
		switch n {
		case 0:
			parts = append(parts, "reset")
		case 1:
			parts = append(parts, "bold")
		case 2:
			parts = append(parts, "dim")
		case 3:
			parts = append(parts, "italic")
		case 4:
			parts = append(parts, "underline")
		case 5:
			parts = append(parts, "blink")
		case 7:
			parts = append(parts, "reverse")
		case 8:
			parts = append(parts, "hidden")
		case 22:
			parts = append(parts, "bold-off")
		case 23:
			parts = append(parts, "italic-off")
		case 24:
			parts = append(parts, "underline-off")
		case 25:
			parts = append(parts, "blink-off")
		case 27:
			parts = append(parts, "reverse-off")
		case 30, 31, 32, 33, 34, 35, 36, 37:
			parts = append(parts, "fg "+ansiColor(n-30))
		case 40, 41, 42, 43, 44, 45, 46, 47:
			parts = append(parts, "bg "+ansiColor(n-40))
		case 90, 91, 92, 93, 94, 95, 96, 97:
			parts = append(parts, "fg-bright "+ansiColor(n-90))
		case 100, 101, 102, 103, 104, 105, 106, 107:
			parts = append(parts, "bg-bright "+ansiColor(n-100))
		case 39:
			parts = append(parts, "fg-default")
		case 49:
			parts = append(parts, "bg-default")
		default:
			parts = append(parts, "sgr"+itoa(n))
		}
	}
	return " (SGR " + strings.Join(parts, " ") + ")"
}

func ansiColor(i int) string {
	switch i {
	case 0:
		return "black"
	case 1:
		return "red"
	case 2:
		return "green"
	case 3:
		return "yellow"
	case 4:
		return "blue"
	case 5:
		return "magenta"
	case 6:
		return "cyan"
	case 7:
		return "white"
	default:
		return "?"
	}
}

func ansiPrivateMode(params []string, final byte) string {
	n, ok := parseInt(params[0])
	if !ok {
		return ""
	}
	val := "off"
	if final == 'h' {
		val = "on"
	}
	switch n {
	case 25:
		return " (cursor visible " + val + ")"
	case 1049:
		return " (alt-screen " + val + ")"
	case 2004:
		return " (bracketed-paste " + val + ")"
	case 1000, 1002, 1003, 1006:
		return " (mouse " + val + ")"
	case 2000, 2001:
		return " (kitty keyboard " + val + ")"
	default:
		return " (DEC " + itoa(n) + " " + val + ")"
	}
}

// ansiSgr renders a glyph's attributes and colors as one SGR sequence.
func ansiSgr(g vt10x.Glyph) string {
	var parts []string
	parts = append(parts, "0")
	if g.Mode&attrBold != 0 {
		parts = append(parts, "1")
	}
	if g.Mode&attrUnderline != 0 {
		parts = append(parts, "4")
	}
	if g.Mode&attrReverse != 0 {
		parts = append(parts, "7")
	}
	if fg := ansiColorCode(g.FG, true); fg != "" {
		parts = append(parts, fg)
	}
	if bg := ansiColorCode(g.BG, false); bg != "" {
		parts = append(parts, bg)
	}
	return ansiCsiSeq(strings.Join(parts, ";"), 'm')
}

// ansiColorCode maps a vt color to its SGR parameter list for fg or bg.
func ansiColorCode(c vt10x.Color, fg bool) string {
	if fg && c == vt10x.DefaultFG || !fg && c == vt10x.DefaultBG {
		if fg {
			return "39"
		}
		return "49"
	}
	if c < 8 {
		if fg {
			return itoa(int(c) + 30)
		}
		return itoa(int(c) + 40)
	}
	if c < 16 {
		if fg {
			return itoa(int(c) + 90 - 8)
		}
		return itoa(int(c) + 100 - 8)
	}
	if c < 256 {
		if fg {
			return "38;5;" + itoa(int(c))
		}
		return "48;5;" + itoa(int(c))
	}
	r := int(c>>16) & 0xff
	g := int(c>>8) & 0xff
	b := int(c) & 0xff
	if fg {
		return "38;2;" + itoa(r) + ";" + itoa(g) + ";" + itoa(b)
	}
	return "48;2;" + itoa(r) + ";" + itoa(g) + ";" + itoa(b)
}
