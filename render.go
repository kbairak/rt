package main

import (
	"bytes"
	"os"
	"strings"

	"golang.org/x/term"
)

func InitTerminal() (func(), error) {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return nil, err
	}
	os.Stdout.WriteString("\x1b[?1049h\x1b[2J\x1b[H\x1b[?2004l")
	return func() {
		os.Stdout.WriteString("\x1b[?25h")
		os.Stdout.WriteString("\x1b[r")
		os.Stdout.WriteString("\x1b[?1049l")
		_ = term.Restore(int(os.Stdin.Fd()), oldState)
	}, nil
}

func Render(store *BlockStore, input string) {
	rows, cols := getTermSize()
	var buf bytes.Buffer

	// scroll region: row 1 is fixed prompt, rows 2..N scroll for blocks
	buf.WriteString("\x1b[2;" + itoa(rows) + "r")
	buf.WriteString("\x1b[H\x1b[2J")
	buf.WriteString("\x1b[?25l") // hide cursor while repainting

	// prompt at row 1
	buf.WriteString("> ")
	buf.WriteString(input)
	buf.WriteString("\x1b[K")
	buf.WriteString("\r\n") // return to col 1 before moving into scroll region

	// blocks newest-first, each preceded by a separator
	blocks := store.All()
	for i := len(blocks) - 1; i >= 0; i-- {
		buf.WriteString(strings.Repeat("─", cols))
		buf.WriteString("\r\n")
		buf.WriteString("> ")
		buf.WriteString(stripANSI(blocks[i].Command))
		buf.WriteString("\r\n")
		out := stripCR(stripANSI(blocks[i].Output))
		out = strings.ReplaceAll(out, "\n", "\r\n")
		out = strings.ReplaceAll(out, "RTMRK", "")
		// strip first line (shell echo of command)
		if idx := strings.Index(out, "\r\n"); idx >= 0 {
			out = out[idx+2:]
		}
		buf.WriteString(out)
		if len(out) > 0 && !strings.HasSuffix(out, "\r\n") {
			buf.WriteString("\r\n")
		}
	}

	// position cursor after prompt for input
	col := 3 + len(input)
	buf.WriteString("\x1b[1;" + itoa(col) + "H")
	buf.WriteString("\x1b[?25h") // show cursor

	os.Stdout.Write(buf.Bytes())
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func stripCR(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = expandTabs(s, 8)
	return s
}

func expandTabs(s string, cols int) string {
	var buf bytes.Buffer
	for _, ch := range s {
		if ch == '\t' {
			col := buf.Len() % cols
			n := cols - col
			for i := 0; i < n; i++ {
				buf.WriteByte(' ')
			}
		} else {
			buf.WriteRune(ch)
		}
	}
	return buf.String()
}

func stripANSI(s string) string {
	var buf bytes.Buffer
	i := 0
	for i < len(s) {
		c := s[i]
		if c == '\x1b' {
			if i+1 < len(s) && s[i+1] == '[' {
				// CSI: skip until final byte (0x40-0x7e)
				j := i + 2
				for j < len(s) && (s[j] < 0x20 || (s[j] >= 0x30 && s[j] <= 0x3f) || s[j] == 0x7f) {
					j++
				}
				if j < len(s) {
					j++ // final byte
				}
				i = j
			} else if i+1 < len(s) && (s[i+1] == ']' || s[i+1] == 'P' || s[i+1] == 'X' || s[i+1] == '_' || s[i+1] == '^') {
				// OSC/DCS/APC/PM/SOS: skip to ST (0x9c, ESC \)
				j := i + 2
				for j < len(s) {
					if s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\' {
						j += 2
						break
					}
					j++
				}
				i = j
			} else if i+1 < len(s) && s[i+1] == '\\' {
				i += 2 // ST alone
			} else {
				// lone ESC sequence (2-char)
				i += 2
			}
		} else {
			buf.WriteByte(s[i])
			i++
		}
	}
	return buf.String()
}

func getTermSize() (rows, cols int) {
	r, c, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil || r <= 0 || c <= 0 {
		return 24, 80
	}
	return r, c
}

func getTermWidth() int {
	_, cols := getTermSize()
	return cols
}