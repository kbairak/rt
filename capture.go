package main

import (
	"bytes"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/hinshun/vt10x"
)

type BlockStore struct {
	mu     sync.Mutex
	blocks []Block
}

func (bs *BlockStore) Add(b Block) {
	bs.mu.Lock()
	bs.blocks = append(bs.blocks, b)
	bs.mu.Unlock()
}

func (bs *BlockStore) All() []Block {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	out := make([]Block, len(bs.blocks))
	copy(out, bs.blocks)
	return out
}

func (bs *BlockStore) Len() int {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	return len(bs.blocks)
}

var marker = []byte("RTMRK")

// blockParser feeds the raw PTY stream into a live vt10x emulator and
// scans the same stream for the prompt marker. When a marker arrives, the
// bytes accumulated since the previous marker are replayed through a fresh
// tall vt10x to produce a frozen archive grid for the finished command.
type blockParser struct {
	live      vt10x.Terminal
	width     atomic.Int32
	rows      atomic.Int32
	store     *BlockStore
	emuMu     sync.Mutex
	buf       bytes.Buffer
	seenFirst bool
	partial   []byte
}

func newBlockParser(store *BlockStore, width int) *blockParser {
	p := &blockParser{
		live:  vt10x.New(vt10x.WithSize(width, 24)),
		store: store,
	}
	p.width.Store(int32(width))
	p.rows.Store(24)
	return p
}

// clearScreen is injected into the live emulator at every command boundary
// so the live region shows only the current command, not the terminal's
// accumulated screen. ESC[2J erases the screen but preserves the cursor,
// which the shell expects at the start of the next prompt.
var clearScreen = []byte(AnsiED)

// Feed writes a PTY chunk to the live emulator and scans it for prompt
// markers. Marker bytes are fed to the emulator (so the prompt keeps its
// screen-relative alignment) but never to the block buffer. At each marker
// the emulator's screen is cleared, so the live region only ever shows the
// current command while finished commands live on as frozen blocks.
func (p *blockParser) Feed(chunk []byte) {
	data := chunk
	if len(p.partial) > 0 {
		data = append(p.partial, chunk...)
		p.partial = nil
	}

	var emu bytes.Buffer
	emu.Grow(len(data) + (len(data)/len(marker)+1)*len(clearScreen))

	i := 0
	for i < len(data) {
		if bytes.HasPrefix(data[i:], marker) {
			p.finalize()
			// clear first, then let the prompt (with its marker) redraw
			// on the cleared line; this keeps the shell's cursor math
			// intact while removing stale screen content
			emu.Write(clearScreen)
			emu.Write(marker)
			i += len(marker)
			continue
		}
		b := data[i]
		p.buf.WriteByte(b)
		emu.WriteByte(b)
		i++
	}

	p.emuWrite(emu.Bytes())

	// save a partial marker at end of data
	for j := len(data) - len(marker) + 1; j < len(data); j++ {
		if j < 0 {
			continue
		}
		suffix := data[j:]
		if hasPartialPrefix(marker, suffix) {
			p.partial = append([]byte(nil), suffix...)
			break
		}
	}
}

// emuWrite writes a chunk to the live emulator while holding the emulator
// mutex. vt10x is unmaintained and can panic on malformed program output;
// we recover by rebuilding a fresh emulator so a stray escape sequence
// degrades gracefully instead of killing the session.
func (p *blockParser) emuWrite(chunk []byte) {
	p.emuMu.Lock()
	defer p.emuMu.Unlock()
	func() {
		defer func() {
			if r := recover(); r != nil {
				p.live = vt10x.New(vt10x.WithSize(int(p.width.Load()), int(p.rows.Load())))
			}
		}()
		_, _ = p.live.Write(chunk)
	}()
}

// Live returns a goroutine-safe view of the live emulator. It must only be
// read, keeping in sync with the reader goroutine via the export/emu locks.
func (p *blockParser) Live() vt10x.View {
	p.emuMu.Lock()
	defer p.emuMu.Unlock()
	return p.live
}

func (p *blockParser) Resize(rows, cols int) {
	p.emuMu.Lock()
	defer p.emuMu.Unlock()
	if rows < 1 {
		rows = 24
	}
	if cols < 1 {
		cols = 80
	}
	p.width.Store(int32(cols))
	p.rows.Store(int32(rows))
	p.live.Resize(cols, rows)
}

func (p *blockParser) finalize() {
	if !p.seenFirst {
		// pre-shell bootstrap output; discard
		p.seenFirst = true
		p.buf.Reset()
		return
	}
	out := replayGrid(p.buf.Bytes(), int(p.width.Load()))
	stripMarkers(out)
	p.store.Add(Block{
		Cells: out,
		Width: int(p.width.Load()),
	})
	p.buf.Reset()
}

func (p *blockParser) Width() int {
	return int(p.width.Load())
}

func hasPartialPrefix(pat, s []byte) bool {
	if len(s) >= len(pat) {
		return bytes.HasPrefix(s, pat)
	}
	return bytes.HasPrefix(pat, s)
}

// replayGrid renders buf through a fresh tall vt10x and returns the
// finished grid, trailing empty rows stripped. Because the emulator is
// taller than the screen, content never scrolls out of view.
func replayGrid(buf []byte, cols int) [][]vt10x.Glyph {
	vt := vt10x.New(vt10x.WithSize(cols, archiveRows))
	_, _ = vt.Write(buf)
	grid, _, _ := snapshotGrid(vt)
	return grid
}

// snapshotGrid copies the emulator's cell buffer under a single lock,
// trailing-trims empty rows, and returns the grid plus the emulator cursor
// position (0-based). Grid is nil if entirely empty.
func snapshotGrid(v vt10x.View) ([][]vt10x.Glyph, int, int) {
	v.Lock()
	defer v.Unlock()
	cols, rows := v.Size()
	cur := v.Cursor()

	grid := make([][]vt10x.Glyph, rows)
	last := -1
	for y := 0; y < rows; y++ {
		row := make([]vt10x.Glyph, cols)
		empty := true
		for x := 0; x < cols; x++ {
			g := v.Cell(x, y)
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
		return nil, cur.X, cur.Y
	}
	return grid[:last+1], cur.X, cur.Y
}

// stripMarker removes the first RTMRK sentinel from a grid row,
// left-shifting the remaining cells so the prompt aligns at column 0.
// Returns the row unchanged if no sentinel is present, with stripped set
// accordingly.
func stripMarker(row []vt10x.Glyph) (out []vt10x.Glyph, stripped bool) {
	for x := 0; x+len(marker) <= len(row); x++ {
		match := true
		for i, b := range marker {
			if row[x+i].Char != rune(b) {
				match = false
				break
			}
		}
		if match {
			out = make([]vt10x.Glyph, len(row)-len(marker))
			copy(out[:x], row[:x])
			copy(out[x:], row[x+len(marker):])
			return out, true
		}
	}
	return row, false
}

// stripMarkers applies stripMarker to every row of a grid, in place.
func stripMarkers(grid [][]vt10x.Glyph) {
	for i, row := range grid {
		out, _ := stripMarker(row)
		grid[i] = out
	}
}

// rowEmpty reports whether a grid row has no painted cells.
func rowEmpty(row []vt10x.Glyph) bool {
	for _, g := range row {
		if g.Char != 0 && g.Char != ' ' {
			return false
		}
	}
	return true
}

// cellText joins a row's glyph runes for logging/matching.
func cellText(row []vt10x.Glyph) string {
	var b strings.Builder
	for _, g := range row {
		if g.Char == 0 {
			b.WriteByte(' ')
		} else {
			b.WriteRune(g.Char)
		}
	}
	return b.String()
}
