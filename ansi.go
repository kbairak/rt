package main

import (
	"fmt"
	"strings"
)

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
			if data[i] == 0x07 {
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
		if c == 0x1b {
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
	if len(seq) < 2 || seq[0] != 0x1b {
		return ""
	}
	if seq[1] == ']' {
		if seq[len(seq)-1] != 0x07 {
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
	params, final := csiParams(seq)
	if len(params) > 0 && strings.HasPrefix(params[0], "?") {
		params[0] = params[0][1:]
		return privateMode(params, final)
	}
	switch final {
	case 'm':
		return sgrName(params)
	case 'A':
		return " (cursor up " + itoa(firstParam(params, 1)) + ")"
	case 'B':
		return " (cursor down " + itoa(firstParam(params, 1)) + ")"
	case 'C':
		return " (cursor right " + itoa(firstParam(params, 1)) + ")"
	case 'D':
		return " (cursor left " + itoa(firstParam(params, 1)) + ")"
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

func firstParam(params []string, def int) int {
	if len(params) < 1 || params[0] == "" {
		return def
	}
	n, ok := parseInt(params[0])
	if !ok {
		return def
	}
	return n
}

func csiParams(seq []byte) ([]string, byte) {
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

func parseInt(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func sgrName(params []string) string {
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

func privateMode(params []string, final byte) string {
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
