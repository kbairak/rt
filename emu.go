package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/hinshun/vt10x"
)

// Glyph attribute bits (vt10x private constants, mirrored).

// compose feeds a buffer chunk into rt, splitting finished commands at the
// prompt marker: bytes before a marker are rendered, snapshotted into history
// with the command's exit code, then the emulator is reset for the next
// command.
func (rt *rtState) compose(data []byte) {
	for len(data) > 0 {
		hi := bytes.Index(data, sepHead)
		if hi < 0 {
			// no marker head; hold any trailing head fragment
			if p := holdLen(data); p > 0 {
				hold := len(data) - p
				if hold > 0 {
					_, _ = rt.vt.Write(data[:hold])
				}
				rt.buffer = append(rt.buffer, data[hold:]...)
			} else {
				_, _ = rt.vt.Write(data)
			}
			return
		}
		rest := data[hi+len(sepHead):]
		j := 0
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
			j++
		}
		if j < len(rest) && rest[j] == sepEnd {
			// complete marker: command ended, exit code in rest[:j]
			if hi > 0 {
				_, _ = rt.vt.Write(data[:hi])
			}
			code, _ := parseInt(string(rest[:j]))
			rt.boundary(code)
			data = rest[j+1:]
			continue
		}
		if j >= len(rest) {
			// marker head (and maybe digits) split across chunks; wait
			if hi > 0 {
				_, _ = rt.vt.Write(data[:hi])
			}
			rt.buffer = append(rt.buffer, data[hi:]...)
			return
		}
		// head followed by a non-digit, non-BEL byte: not a real marker
		if hi+1 < len(data) {
			_, _ = rt.vt.Write(data[:hi+1])
			data = data[hi+1:]
		} else {
			_, _ = rt.vt.Write(data)
			return
		}
	}
}

// holdLen returns the length of the longest suffix of b that is a proper
// prefix of sepHead (0 if none). Detects a marker head split across two chunks.
func holdLen(b []byte) int {
	limit := len(sepHead) - 1
	if limit > len(b) {
		limit = len(b)
	}
	for l := limit; l > 0; l-- {
		if bytes.HasSuffix(b[:l], sepHead[:l]) {
			return l
		}
	}
	return 0
}

// boundary runs when a prompt marker arrives: the just-finished command's grid
// is frozen into history with its exit code (unless this is the first,
// bootstrap, prompt).
func (rt *rtState) boundary(code int) {
	rt.log.event("sep")
	rt.dirty = true
	if !rt.first {
		g, _, _ := rt.snapshot()
		if g != nil {
			rt.his = append(rt.his, block{cells: g, width: rt.width, code: code})
			rt.log.event("block " + itoa(len(rt.his)))
		}
	} else {
		rt.first = false
	}
	rt.vt = vt10x.New(vt10x.WithSize(rt.width, rt.height))
}

// snapshot copies the emulator grid, trimming trailing empty rows, and
// returns it plus the cursor position. Returns nil grid if nothing painted.
func (rt *rtState) snapshot() ([][]vt10x.Glyph, int, int) {
	vt := rt.vt
	vt.Lock()
	defer vt.Unlock()
	cols, rows := vt.Size()
	cur := vt.Cursor()
	cx, cy := cur.X, cur.Y

	grid := make([][]vt10x.Glyph, rows)
	last := -1
	for y := 0; y < rows; y++ {
		row := make([]vt10x.Glyph, cols)
		empty := true
		for x := 0; x < cols; x++ {
			g := vt.Cell(x, y)
			row[x] = g
			if g.Char != 0 && g.Char != ' ' {
				empty = false
			}
		}
		grid[y] = row
		if !empty {
			last = y
		}
	}
	if last < 0 {
		return nil, cx, cy
	}
	return grid[:last+1], cx, cy
}

// repaint draws the full frame: live emulator grid, then history blocks
// newest-first with a header row, cropped to terminal height.
func (rt *rtState) repaint() {
	var frame bytes.Buffer
	rows, cols := rt.height, rt.width

	grid, cx, cy := rt.snapshot()

	var line bytes.Buffer
	grow := func(y int, row []vt10x.Glyph, width int) {
		line.Reset()
		cellRow(&line, row, width)
		fmt.Fprintf(&frame, "%s%s", cursorPos(y, 0), line.Bytes())
	}

	y := 0
	if grid != nil {
		for r := 0; r < len(grid); r++ {
			if y >= rows {
				break
			}
			grow(y, grid[r], cols)
			y++
		}
	}

	for k := len(rt.his) - 1; k >= 0; k-- {
		if y >= rows {
			break
		}
		b := rt.his[k]
		var sep bytes.Buffer
		sep.WriteString("─ [" + itoa(b.code) + "] ")
		for i := len(sep.Bytes()); i < cols; i++ {
			sep.WriteString("─")
		}
		fmt.Fprintf(&frame, "%s%s", cursorPos(y, 0), sep.Bytes())
		y++
		for r := 0; r < len(b.cells); r++ {
			if y >= rows {
				break
			}
			width := b.width
			if width > cols {
				width = cols
			}
			grow(y, b.cells[r], width)
			y++
		}
	}

	// clear, draw, plant cursor at the live emulator cursor
	os.Stdout.WriteString("\x1b[?25l\x1b[H\x1b[2J")
	os.Stdout.Write(frame.Bytes())
	os.Stdout.WriteString(cursorPos(cy, cx) + "\x1b[?25h")
}

const (
	attrReverse   = 1 << 0
	attrUnderline = 1 << 1
	attrBold      = 1 << 2
)

// cellRow renders one grid row as ANSI: SGR emitted only when attributes or
// colors change from the previous cell, truncated to width.
func cellRow(buf *bytes.Buffer, row []vt10x.Glyph, width int) {
	var last vt10x.Glyph
	first := true
	for x := 0; x < width && x < len(row); x++ {
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
	buf.WriteString("\x1b[0m")
}

func sgr(g vt10x.Glyph) string {
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
	if fg := colorCode(g.FG, true); fg != "" {
		parts = append(parts, fg)
	}
	if bg := colorCode(g.BG, false); bg != "" {
		parts = append(parts, bg)
	}
	return "\x1b[" + strings.Join(parts, ";") + "m"
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

func cursorPos(row, col int) string {
	return "\x1b[" + itoa(row+1) + ";" + itoa(col+1) + "H"
}

var _ = sync.Mutex{}
