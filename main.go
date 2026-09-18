package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

type event struct {
	kind string
	data []byte
	at   time.Duration
}

func main() {
	cmd := exec.Command("sh")
	cmd.Env = append(os.Environ(), "ENV=/dev/null")

	master, err := pty.Start(cmd)
	if err != nil {
		panic(err)
	}

	start := time.Now()
	events := make(chan event, 1024)

	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := master.Read(buf)
			if n > 0 {
				events <- event{"RX", append([]byte{}, buf[:n]...), time.Since(start)}
			}
			if rerr != nil {
				close(events)
				return
			}
		}
	}()

	// for _, cmdString := range []string{"export PS1=RTSEP$PS1\r", "echo 1\r", "echo 2\r", "exit\r"} {
	for _, cmdString := range []string{"echo 1\r", "echo 2\r", "exit\r"} {
		time.Sleep(100 * time.Millisecond)
		events <- event{"TX", []byte(cmdString), time.Since(start)}
		_, _ = master.Write([]byte(cmdString))
	}

	var rxStream bytes.Buffer
	txIdx, rxIdx := 0, 0
	for e := range events {
		data := e.data
		if e.kind == "RX" {
			rxStream.Write(data)
		}
		for i := 0; i < len(data); {
			b := data[i]
			switch {
			case b == 0x1b:
				// ANSI escape sequence (whole sequence as one line)
				n := ansiSeqLen(data[i:])
				seq := data[i : i+n]
				start, end := bumpIdx(e.kind, &txIdx, &rxIdx, n)
				emit(e, start, end, "ANSI", ansiRepr(seq)+ansiMeaning(seq))
				i += n
			case b >= 0x80:
				// multibyte unicode character
				r, size := utf8.DecodeRune(data[i:])
				start, end := bumpIdx(e.kind, &txIdx, &rxIdx, size)
				emit(e, start, end, "MULTIBYTE", fmt.Sprintf("%s U+%04X", string(r), r))
				i += size
			case b >= 0x21:
				// printable ascii run (excluding space)
				j := i
				for j < len(data) && data[j] >= 0x21 {
					j++
				}
				start, end := bumpIdx(e.kind, &txIdx, &rxIdx, j-i)
				emit(e, start, end, "TEXT", string(data[i:j]))
				i = j
			default:
				// control byte / space: group identical runs, label+count
				j := i
				for j < len(data) && data[j] == b {
					j++
				}
				start, end := bumpIdx(e.kind, &txIdx, &rxIdx, j-i)
				n := j - i
				name := label(b)
				if n > 1 {
					name += fmt.Sprintf(" %dx", n)
				}
				emit(e, start, end, "ASCII", name)
				i = j
			}
		}
	}

	_ = master.Close()
	state, _ := cmd.Process.Wait()
	fmt.Printf("\npty read side closed, sh exited with code %d\n", state.ExitCode())

	fmt.Printf("\nvt10x grid after replaying RX stream (%d bytes):\n", rxStream.Len())
	printGrid(rxStream.Bytes())
}

func printGrid(stream []byte) {
	vt := vt10x.New(vt10x.WithSize(80, 24))
	_, _ = vt.Write(stream)
	vt.Lock()
	defer vt.Unlock()
	cols, rows := vt.Size()
	for y := 0; y < rows; y++ {
		// find rightmost painted cell
		last := -1
		for x := 0; x < cols; x++ {
			if painted(vt.Cell(x, y)) {
				last = x
			}
		}
		if last < 0 {
			continue
		}
		for x := 0; x <= last; x++ {
			g := vt.Cell(x, y)
			if g.Char == 0 {
				fmt.Print(" ")
			} else {
				fmt.Printf("%c", g.Char)
			}
		}
		fmt.Println()
	}
}

func painted(g vt10x.Glyph) bool {
	return g.Char != 0 && g.Char != ' '
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
		return "CTRL " + fmt.Sprintf("0x%02x", b)
	}
}

// ms formats an elapsed duration as fixed-width milliseconds.
func ms(d time.Duration) string {
	return fmt.Sprintf("%7.1f", float64(d.Microseconds())/1000.0)
}

// emit prints one log line: <timestamp> side idx|idx-idx TYPE payload.
func emit(e event, start, end int, typ, payload string) {
	s := fmt.Sprintf("%d-%d", start, end)
	if start == end {
		s = fmt.Sprintf("%d", start)
	}
	fmt.Printf("%sms %-2s %9s %-9s %s\n", ms(e.at), e.kind, s, typ, payload)
}

// bumpIdx advances the running per-direction index counters for n bytes
// and returns the 1-based start/end indices of that span.
func bumpIdx(kind string, txIdx, rxIdx *int, n int) (start, end int) {
	if kind == "TX" {
		*txIdx += n
		return *txIdx - n + 1, *txIdx
	}
	*rxIdx += n
	return *rxIdx - n + 1, *rxIdx
}

// ansiSeqLen returns the length of the escape sequence starting at data[0],
// which must be ESC. Handles CSI (ESC[…final), OSC (ESC]…BEL), and the
// two-char baseline (ESC + one byte). Truncates to the buffer if
// unterminated.
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

// ansiRepr renders an escape sequence for the log's ascii column: ESC is
// shown as "ESC", other printable bytes as-is, controls as <XX> hex.
func ansiRepr(seq []byte) string {
	var b strings.Builder
	for _, c := range seq {
		switch c {
		case 0x1b:
			b.WriteString("ESC")
		default:
			if c >= 0x20 && c < 0x7f {
				b.WriteRune(rune(c))
			} else {
				b.WriteString("‹" + fmt.Sprintf("%02x", c) + "›")
			}
		}
	}
	return b.String()
}

// ansiMeaning decodes a CSI sequence into a human-readable description,
// or "" if unknown. Used to annotate log lines for known sequences.
func ansiMeaning(seq []byte) string {
	if len(seq) < 2 || seq[0] != 0x1b {
		return ""
	}
	if seq[1] == ']' {
		return " (OSC)"
	}
	if seq[1] != '[' {
		return " (ESC single)"
	}
	// seq = ESC [ params final
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

// firstParam returns params[i] parsed as int, or def when absent/garbage.
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

// csiParams splits "ESC[<digits;digits>final" into its ';'-separated param
// strings and the final byte.
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
	var b []byte
	if n == 0 {
		return "0"
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// sgrName maps an SGR parameter list to a human description.
func sgrName(params []string) string {
	var parts []string
	for i := 0; i < len(params); i++ {
		n, ok := parseInt(params[i])
		if !ok {
			continue
		}
		// 38;5;N / 48;5;N consume extra params
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

// privateMode decodes DEC private modes (ESC[?...P h/l).
func privateMode(params []string, final byte) string {
	n, ok := parseInt(params[0])
	if !ok {
		return ""
	}
	on := final == 'h'
	val := "off"
	if on {
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
