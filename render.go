package main

import (
	"bytes"
	"os"
	"strings"

	"github.com/hinshun/vt10x"
	"golang.org/x/term"
)

// InitTerminal puts stdin in raw mode (keystrokes delivered unprocessed,
// no echo) and enters the alternate screen buffer so rt's renders replace
// the user's screen and are fully restored on exit. Bracketed paste is
// disabled so pasted text arrives unwrapped (rt's renderer owns paste
// handling, not the terminal). Returns a restore function that undoes all
// of this; callers must run it before exiting.
func InitTerminal() (func(), error) {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return nil, err
	}
	os.Stdout.WriteString(AnsiAltOn + AnsiED + AnsiHome + AnsiBracketOff)
	return func() {
		os.Stdout.WriteString(AnsiShowCursor + AnsiResetScroll + AnsiAltOff)
		_ = term.Restore(int(os.Stdin.Fd()), oldState)
	}, nil
}

// Glyph attribute bits mirrored from vt10x's unexported attr* constants.
const (
	attrReverse   = 1 << 0
	attrUnderline = 1 << 1
	attrBold      = 1 << 2
	attrItalic    = 1 << 4
	attrBlink     = 1 << 5
)

// separator between historical blocks
const sepChar = "─"

// Render composes the full frame: the live emulator grid at the top,
// then a separator + frozen grid per historical block, newest first.
// Past grids are never re-rendered through the emulator; if the terminal
// is narrower than a saved grid, the grid is cropped.
func Render(store *BlockStore, live vt10x.View) {
	rows, cols := getTermSize()
	liveGrid, cx, cy := snapshotGrid(live)
	// trim leading empty rows: the live emulator's screen is cleared at
	// each command boundary, so everything before the current prompt is
	// blank and must not consume frame rows.
	top := 0
	for top < len(liveGrid) && rowEmpty(liveGrid[top]) {
		top++
	}
	liveGrid = liveGrid[top:]
	cy -= top
	if cy < 0 {
		cy = 0
	}

	var buf bytes.Buffer
	buf.WriteString(AnsiHome + AnsiED + AnsiHideCursor) // repaint start

	y := 0
	curY, curX := 0, 0
	if len(liveGrid) > 0 {
		for y < len(liveGrid) {
			if y >= rows {
				break
			}
			row, stripped := stripMarker(liveGrid[y])
			if y == cy && stripped && cx >= len(marker) {
				cx -= len(marker)
			}
			renderRow(&buf, row, cols, y)
			y++
		}
		curX, curY = cx, cy
	}

	blocks := store.All()
	for i := len(blocks) - 1; i >= 0; i-- {
		b := blocks[i]
		if y >= rows {
			break
		}
		sep := cursorPos(y, 0) + strings.Repeat(sepChar, cols)
		buf.WriteString(sep)
		y++
		for _, row := range b.Cells {
			if y >= rows {
				break
			}
			renderRow(&buf, row, cols, y)
			y++
		}
	}

	// plant the cursor back on the live screen
	if curY < 0 {
		curY = 0
	}
	if curX < 0 {
		curX = 0
	}
	if curY >= rows {
		curY = rows - 1
	}
	if curX >= cols {
		curX = cols - 1
	}
	buf.WriteString(cursorPos(curY, curX))
	buf.WriteString(AnsiShowCursor) // show cursor

	os.Stdout.Write(buf.Bytes())
}

// renderRow paints one grid row at absolute row y (0-based), truncated to
// width w. Emits SGR only when the cell attributes change from the last
// emitted cell.
func renderRow(buf *bytes.Buffer, row []vt10x.Glyph, w, y int) {
	buf.WriteString(cursorPos(y, 0))
	var last vt10x.Glyph
	first := true
	for x := 0; x < w && x < len(row); x++ {
		g := row[x]
		if first || g.FG != last.FG || g.BG != last.BG || g.Mode != last.Mode {
			buf.WriteString(sgr(g))
			last = g
			first = false
		}
		c := g.Char
		if c == 0 {
			c = ' '
		}
		buf.WriteRune(c)
	}
	buf.WriteString(AnsiSGRReset)
}

func sgr(g vt10x.Glyph) string {
	var parts []string
	parts = append(parts, "0")
	if g.Mode&attrBold != 0 {
		parts = append(parts, "1")
	}
	if g.Mode&attrItalic != 0 {
		parts = append(parts, "3")
	}
	if g.Mode&attrUnderline != 0 {
		parts = append(parts, "4")
	}
	if g.Mode&attrBlink != 0 {
		parts = append(parts, "5")
	}
	if g.Mode&attrReverse != 0 {
		parts = append(parts, "7")
	}
	if fg := colorCode(g.FG, true); fg != "" {
		parts = append(parts, fg)
	}
	if bg := colorCode(g.BG, false); bg != "" {
		parts = append(parts, bg)
	}
	return sgrSeq(parts)
}

func colorCode(c vt10x.Color, fg bool) string {
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
	if c < 1<<24 {
		// xterm 256-color
		if fg {
			return "38;5;" + itoa(int(c))
		}
		return "48;5;" + itoa(int(c))
	}
	// truecolor
	r := int(c>>16) & 0xff
	g := int(c>>8) & 0xff
	b := int(c) & 0xff
	if fg {
		return "38;2;" + itoa(r) + ";" + itoa(g) + ";" + itoa(b)
	}
	return "48;2;" + itoa(r) + ";" + itoa(g) + ";" + itoa(b)
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

func getTermSize() (rows, cols int) {
	r, c, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil || r <= 0 || c <= 0 {
		return 24, 80
	}
	return r, c
}
