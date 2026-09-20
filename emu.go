package main

import (
	"bytes"
	"encoding/binary"
	"hash/fnv"
	"strings"

	"github.com/hinshun/vt10x"
)

// Glyph attribute bits (vt10x private constants, mirrored).

// composeChunk feeds a buffer chunk into vt, splitting finished commands at the
// prompt marker: bytes before a marker are rendered, the boundary callback runs
// with the command's exit code, then the emulator is reset for the next command.
// Bytes that may be a split marker head are returned to be re-buffered.
func composeChunk(vtp *vt10x.Terminal, data []byte, boundary func(code int)) []byte {
	for len(data) > 0 {
		hi := bytes.Index(data, ansiSepHead)
		if hi < 0 {
			// no marker head; holdIter already guarantees no head
			// fragment trails the chunk
			_, _ = (*vtp).Write(data)
			return nil
		}
		rest := data[hi+len(ansiSepHead):]
		j := 0
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
			j++
		}
		if j < len(rest) && rest[j] == ansiSepEnd {
			// complete marker: command ended, exit code in rest[:j]
			if hi > 0 {
				_, _ = (*vtp).Write(data[:hi])
			}
			code, _ := parseInt(string(rest[:j]))
			boundary(code)
			data = rest[j+1:]
			continue
		}
		if j >= len(rest) {
			// marker head (and maybe digits) split across chunks; wait
			if hi > 0 {
				_, _ = (*vtp).Write(data[:hi])
			}
			return append([]byte(nil), data[hi:]...)
		}
		// head followed by a non-digit, non-BEL byte: not a real marker
		if hi+1 < len(data) {
			_, _ = (*vtp).Write(data[:hi+1])
			data = data[hi+1:]
		} else {
			_, _ = (*vtp).Write(data)
			return nil
		}
	}
	return nil
}

// trackAltScreen sets *inAlt from complete DEC 1049 enter/leave sequences in
// data. Sequences arrive whole thanks to holdReader, but a trailing header
// prefix is guarded anyway.
func trackAltScreen(data []byte, inAlt *bool) {
	from := 0
	for {
		j := indexFrom(data, []byte(ansiAltPrefix), from)
		if j < 0 {
			return
		}
		k := j + len(ansiAltPrefix)
		if k >= len(data) {
			return
		}
		switch data[k] {
		case 'h':
			*inAlt = true
		case 'l':
			*inAlt = false
		}
		from = k + 1
	}
}

// boundary runs when a prompt marker arrives: the just-finished command's grid
// is frozen into history with its exit code (unless this is the first,
// bootstrap, prompt).
var (
	blockHasher = fnv.New64a()
	buf8        [8]byte
	buf4        [4]byte
	buf2        [2]byte
)

// newBlock wraps a finished grid with its exit code and a cached content hash.
// Every history append MUST go through here so blocksEqual's hash fast path
// works; a hand-built literal leaves hash zero and fast-rejects everything.
func newBlock(g [][]vt10x.Glyph, width, code int) block {
	return block{cells: g, width: width, code: code, count: 1, hash: blockHash(g)}
}

// blockHash folds the grid (dims + every glyph's Char/FG/BG/Mode) into a
// 64-bit FNV-1a. Exit code is not included: it is metadata, not content.
func blockHash(cells [][]vt10x.Glyph) uint64 {
	blockHasher.Reset()
	for _, row := range cells {
		binary.LittleEndian.PutUint64(buf8[:], uint64(len(row)))
		blockHasher.Write(buf8[:])
		for _, g := range row {
			binary.LittleEndian.PutUint64(buf8[:], uint64(g.Char))
			blockHasher.Write(buf8[:])
			binary.LittleEndian.PutUint16(buf2[:], uint16(g.Mode))
			blockHasher.Write(buf2[:])
			binary.LittleEndian.PutUint32(buf4[:], uint32(g.FG))
			blockHasher.Write(buf4[:])
			binary.LittleEndian.PutUint32(buf4[:], uint32(g.BG))
			blockHasher.Write(buf4[:])
		}
	}
	return blockHasher.Sum64()
}

// blocksEqual reports whether a and b are identical grids: same dimensions
// and same Char/FG/BG/Mode in every cell. Exit code is not compared. The
// cached hash rejects most pairs in O(1); the cell walk runs only when the
// hashes collide (guarantees no false collapse, bilaterally).
func blocksEqual(a, b block) bool {
	if a.hash != b.hash {
		return false
	}
	if len(a.cells) != len(b.cells) {
		return false
	}
	for y := range a.cells {
		ra, rb := a.cells[y], b.cells[y]
		if len(ra) != len(rb) {
			return false
		}
		for x := range ra {
			if ra[x] != rb[x] {
				return false
			}
		}
	}
	return true
}

// snapshotGrid copies the emulator grid, trimming trailing empty rows, and
// returns it plus the cursor position. Returns nil grid if nothing painted.
func snapshotGrid(vt vt10x.Terminal) ([][]vt10x.Glyph, int, int) {
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

// compose builds every screen row as ANSI bytes. With ov == nil it renders the
// live emulator grid first, then history blocks newest-first with a header row,
// cropped to height. With ov != nil (copy mode) the live grid is hidden, every
// history block is shifted one column right into a gutter, the selected block
// gets a green vertical line in that gutter, and the bottom row is the status
// line. ov.scroll is the first history line shown (0 = newest entry's
// separator). Rows past the content are nil. It also returns the live emulator
// cursor position.
func compose(his []block, vt vt10x.Terminal, width, height int, ov *overlay) ([][]byte, int, int) {
	rows := make([][]byte, height)
	grid, cx, cy := snapshotGrid(vt)

	// In copy mode the leftmost column is a gutter, the bottom row is the
	// status line, history is scrolled by ov.scroll, and blocks may be
	// collapsed to their first collapsedLines content rows plus an indicator.
	contentW := width
	limit := height
	skip := 0
	collapsed := false
	if ov != nil {
		if contentW = width - 1; contentW < 0 {
			contentW = 0
		}
		limit--
		skip = ov.scroll
		collapsed = ov.collapsed
	}

	var line bytes.Buffer
	put := func(y int, row []vt10x.Glyph, w int, gutter string) {
		line.Reset()
		line.WriteString(gutter)
		cellRow(&line, row, w)
		rows[y] = append([]byte(nil), line.Bytes()...)
	}

	y := 0
	if ov == nil && grid != nil {
		for r := 0; r < len(grid) && y < height; r++ {
			put(y, grid[r], width, "")
			y++
		}
	}

	pos := 0 // running line index of the current entry's separator
	for k := len(his) - 1; k >= 0 && y < limit; k-- {
		b := his[k]
		span := entryHeight(b, collapsed)
		start := pos
		pos += span
		if start+span-1 < skip {
			continue // entirely above the viewport
		}
		skipEntry := 0
		if skip > start {
			skipEntry = skip - start
		}
		rep := b.count
		if rep < 1 {
			rep = 1
		}
		gutter := ""
		if ov != nil {
			gutter = " "
			if k == ov.sel {
				gutter = ansiGreen + gutterLine + ansiReset
			}
		}
		shown := len(b.cells)
		capped := false
		if collapsed && shown > collapsedLines {
			shown, capped = collapsedLines, true
		}
		if skipEntry == 0 {
			rows[y] = append([]byte(gutter), sepText(rep, b.code, contentW)...)
			y++
		}
		r0 := 0
		if skipEntry >= 1 {
			r0 = skipEntry - 1
		}
		if r0 > shown {
			r0 = shown
		}
		for r := r0; r < shown && y < limit; r++ {
			w := b.width
			if w > contentW {
				w = contentW
			}
			put(y, b.cells[r], w, gutter)
			y++
		}
		if capped && y < limit {
			rows[y] = append([]byte(gutter), cropText(moreText(len(b.cells)-shown), contentW)...)
			y++
		}
	}

	if ov != nil && height > 0 {
		rows[height-1] = []byte(overlayStatus(ov, len(his), width))
	}
	return rows, cx, cy
}

// renderer diffs successive frames and emits only the rows that changed,
// wrapped in a synchronized update. It is owned by the loop goroutine, like
// vt and history.
type renderer struct {
	prev   [][]byte
	width  int
	height int
	init   bool
}

// frame composes the current screen and returns the minimal byte stream that
// brings the real terminal up to date: a full clear on the first frame or a
// size change, otherwise only the changed rows. The cursor position is always
// emitted. The whole update is wrapped in DEC 2026 so capable terminals render
// it atomically; others ignore the wrapper.
func (r *renderer) frame(his []block, vt vt10x.Terminal, width, height int, ov *overlay) []byte {
	rows, cx, cy := compose(his, vt, width, height, ov)

	var body bytes.Buffer
	if !r.init || width != r.width || height != r.height || len(r.prev) != height {
		body.WriteString(ansiClearScreen)
		r.prev = make([][]byte, height)
		r.init = true
	}
	r.width, r.height = width, height

	body.WriteString(ansiHideCursor)
	for y := 0; y < height; y++ {
		var cur []byte
		if y < len(rows) {
			cur = rows[y]
		}
		if bytes.Equal(cur, r.prev[y]) {
			continue
		}
		body.WriteString(ansiCursorPos(y, 0))
		body.Write(cur)
		body.WriteString(ansiClearLine)
		r.prev[y] = append([]byte(nil), cur...)
	}
	body.WriteString(ansiCursorPos(cy, cx) + ansiShowCursor)

	frame := make([]byte, 0, len(body.Bytes())+len(ansiSyncBegin)+len(ansiSyncEnd))
	frame = append(frame, ansiSyncBegin...)
	frame = append(frame, body.Bytes()...)
	frame = append(frame, ansiSyncEnd...)
	return frame
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
			buf.WriteString(ansiSgr(g))
			last = g
			first = false
		}
		c := g.Char
		if c == 0 {
			c = ' '
		}
		buf.WriteRune(c)
	}
	buf.WriteString(ansiReset)
}

// sepText renders a block-run separator filling the full width: "────…─── [code] -"
// for a run of one, "────…── 3x [code] -" for a collapsed run of N. Info is
// right-aligned so both forms look the same length and left edges line up.
func sepText(rep, code, cols int) string {
	suffix := " [" + itoa(code) + "] -"
	if rep > 1 {
		suffix = " " + itoa(rep) + "x [" + itoa(code) + "] -"
	}
	n := cols - len(suffix)
	if n < 0 {
		n = 0
	}
	return strings.Repeat("─", n) + suffix
}

// blockText renders a block's grid as plain text (no colors or styling):
// trailing blanks are trimmed from each row and the rows are joined with "\n".
func blockText(cells [][]vt10x.Glyph) string {
	if len(cells) == 0 {
		return ""
	}
	lines := make([]string, len(cells))
	for y, row := range cells {
		last := -1
		for x, g := range row {
			if g.Char != 0 && g.Char != ' ' {
				last = x
			}
		}
		var b strings.Builder
		for x := 0; x <= last; x++ {
			c := row[x].Char
			if c == 0 {
				c = ' '
			}
			b.WriteRune(c)
		}
		lines[y] = b.String()
	}
	return strings.Join(lines, "\n")
}
