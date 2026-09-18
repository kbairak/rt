package main

import (
	"bufio"
	"io"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hinshun/vt10x"
)

// mkGrid builds a cols-wide grid. Prefer it over hand-written block literals:
// indices into equality tests must come from newBlock, never a literal, so
// the cached hash is real.
func mkGrid(rows, cols int) [][]vt10x.Glyph {
	g := make([][]vt10x.Glyph, rows)
	for y := range g {
		g[y] = make([]vt10x.Glyph, cols)
	}
	return g
}

func set(g [][]vt10x.Glyph, x, y int, c rune, fg, bg vt10x.Color) {
	g[y][x] = vt10x.Glyph{Char: c, FG: fg, BG: bg}
}

func TestBlockHash(t *testing.T) {
	g := mkGrid(2, 3)
	set(g, 0, 0, 'a', 1, 2)
	set(g, 2, 1, 'b', 3, 4)

	a := blockHash(g)
	b := blockHash(g)
	if a != b {
		t.Fatalf("identical grids: hash %d != %d", a, b)
	}

	// one Char flipped
	g2 := mkGrid(2, 3)
	copyRow := func(dst, src [][]vt10x.Glyph) {
		for y := range src {
			copy(dst[y], src[y])
		}
	}
	copyRow(g2, g)
	set(g2, 0, 0, 'z', 1, 2)
	if blockHash(g2) == a {
		t.Fatal("changed Char: hash must differ")
	}

	// one FG change
	g3 := mkGrid(2, 3)
	copyRow(g3, g)
	set(g3, 2, 1, 'b', 9, 4)
	if blockHash(g3) == a {
		t.Fatal("changed FG: hash must differ")
	}

	// one BG change
	g4 := mkGrid(2, 3)
	copyRow(g4, g)
	set(g4, 2, 1, 'b', 3, 9)
	if blockHash(g4) == a {
		t.Fatal("changed BG: hash must differ")
	}

	// one Mode change
	g5 := mkGrid(2, 3)
	copyRow(g5, g)
	gg := g5[1][2]
	g5[1][2] = vt10x.Glyph{Char: gg.Char, Mode: gg.Mode + 1, FG: gg.FG, BG: gg.BG}
	if blockHash(g5) == a {
		t.Fatal("changed Mode: hash must differ")
	}

	// same grid, different code: not our concern here, hash ignores code.
	b1 := newBlock(g, 5, 0)
	b2 := newBlock(g, 5, 7)
	if b1.hash != b2.hash {
		t.Fatal("code must not affect hash")
	}
}

func TestBlocksEqual(t *testing.T) {
	base := func() block {
		g := mkGrid(2, 3)
		set(g, 0, 0, 'a', 1, 2)
		set(g, 2, 1, 'b', 3, 4)
		return newBlock(g, 5, 0)
	}
	clone := func(src block) block {
		g := mkGrid(len(src.cells), len(src.cells[0]))
		for y := range src.cells {
			copy(g[y], src.cells[y])
		}
		return newBlock(g, src.width, src.code)
	}

	cases := []struct {
		name  string
		mut   func(block) block
		equal bool
	}{
		{"identical", func(b block) block { return clone(b) }, true},
		{"char", func(b block) block { n := clone(b); set(n.cells, 0, 0, 'z', 1, 2); return n }, false},
		{"fg", func(b block) block { n := clone(b); set(n.cells, 0, 0, 'a', 9, 2); return n }, false},
		{"bg", func(b block) block { n := clone(b); set(n.cells, 0, 0, 'a', 1, 9); return n }, false},
		{"mode", func(b block) block { n := clone(b); gg := n.cells[0][0]; n.cells[0][0] = vt10x.Glyph{Char: gg.Char, Mode: gg.Mode + 1, FG: gg.FG, BG: gg.BG}; return n }, false},
		{"rowcount", func(b block) block { n := clone(b); n.cells = n.cells[:1]; return n }, false},
		{"rowwidth", func(b block) block { n := clone(b); n.cells[0] = n.cells[0][:2]; return n }, false},
		{"code", func(b block) block { n := clone(b); n.code = 7; return n }, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := blocksEqual(base(), c.mut(base())); got != c.equal {
				t.Fatalf("blocksEqual = %v, want %v", got, c.equal)
			}
		})
	}
}

func TestCollapsedSeparator(t *testing.T) {
	if s := sepText(1, 7, 80); !strings.HasSuffix(s, " [7] -") {
		t.Fatalf("rep 1: %q", s)
	}
	if s := sepText(3, 0, 20); !strings.HasSuffix(s, " 3x [0] -") {
		t.Fatalf("rep 3: %q", s)
	}
	// exactly cols display columns: info at the end, ─ fill up front.
	for _, c := range []struct{ rep, code, cols int }{
		{1, 0, 10},
		{3, 0, 20},
		{12, 7, 40},
	} {
		s := sepText(c.rep, c.code, c.cols)
		if w := displayWidth(s); w != c.cols {
			t.Fatalf("sepText(%d,%d,%d) width %d != %d: %q", c.rep, c.code, c.cols, w, c.cols, s)
		}
	}
}

// displayWidth counts runes; every separator character is one column.
func displayWidth(s string) int {
	return utf8.RuneCountInString(s)
}

func TestHoldPrefixes(t *testing.T) {
	cases := []struct {
		name string
		b    string
		pfx  []string
		want int
	}{
		{"suffix SEP", "abcSEP", []string{"SEPARATOR"}, 3},
		{"empty pfx", "SEP", []string{""}, 0},
		{"esc suffix", "hello \x1b", []string{"\x1b[?1049h", "\x1b[?1049l"}, 1},
		{"complete not proper", "SEPARATOR", []string{"SEPARATOR"}, 0},
		{"non-matching end", "SEPx", []string{"SEPARATOR"}, 0},
		{"empty b", "", []string{"SEPARATOR"}, 0},
		{"longer overlap wins", "x\x1b[?1049", []string{"\x1b[?1049h", "\x1b[?1049l"}, 7},
		{"zero-length pfx", "SEP", []string{""}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var pfx [][]byte
			for _, s := range c.pfx {
				pfx = append(pfx, []byte(s))
			}
			if got := holdPrefixes([]byte(c.b), pfx); got != c.want {
				t.Fatalf("holdPrefixes(%q) = %d, want %d", c.b, got, c.want)
			}
		})
	}
}

func TestTrackAltScreen(t *testing.T) {
	cases := []struct {
		name string
		data string
		want bool
	}{
		{"enter only", "\x1b[?1049h", true},
		{"leave only", "\x1b[?1049l", false},
		{"enter then leave", "\x1b[?1049h\x1b[?1049l", false},
		{"double enter then leave", "\x1b[?1049h\x1b[?1049h\x1b[?1049l", false},
		{"header then x unchanged", "\x1b[?1049x", false},
		{"trailing prefix guarded", "\x1b[?1049", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inAlt := false
			trackAltScreen([]byte(c.data), &inAlt)
			if inAlt != c.want {
				t.Fatalf("trackAltScreen(%q) leaves %v, want %v", c.data, inAlt, c.want)
			}
		})
	}
}

func TestSwallowCtrlL(t *testing.T) {
	idle := func() *rtState {
		return &rtState{}
	}
	cases := []struct {
		name   string
		rt     *rtState
		data   string
		swallow bool
	}{
		{"idle pure ctrl-l", idle(), "\x0c", true},
		{"double ctrl-l", idle(), "\x0c\x0c", true},
		{"bootstrap prompt", &rtState{first: true}, "\x0c", false},
		{"buffer non-empty", &rtState{buffer: []byte("x")}, "\x0c", false},
		{"in alt-screen", &rtState{inAltScreen: true}, "\x0c", false},
		{"pasted mixed", idle(), "\x0cA", false},
		{"empty read", idle(), "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.rt.swallowCtrlL([]byte(c.data))
			if got != c.swallow {
				t.Fatalf("swallowCtrlL = %v, want %v", got, c.swallow)
			}
			if c.swallow != (c.rt.clearReq) {
				t.Fatalf("clearReq = %v, want %v", c.rt.clearReq, c.swallow)
			}
		})
	}
}

func TestComposeMarkerSplit(t *testing.T) {
	rt := &rtState{
		vt:     vt10x.New(vt10x.WithSize(80, 24)),
		width:  80,
		height: 24,
		first:  false,
		log:    &recorder{init: time.Now(), w: bufio.NewWriter(io.Discard)},
	}
	watched := [][]byte{sepHead, []byte(enterAlt), []byte(leaveAlt)}
	held := newHoldReader(&chunkReader{chunks: [][]byte{
		[]byte("..cmd\n" + string(sepHead) + "0"),
		[]byte("\x07> "),
	}}, func(b []byte) int {
		return holdPrefixes(b, watched)
	})

	p := make([]byte, 4096)
	for {
		n, err := held.Read(p)
		if n > 0 {
			rt.buffer = append(rt.buffer, p[:n]...)
			buf := rt.buffer
			rt.buffer = nil
			rt.compose(buf)
		}
		if err != nil {
			break
		}
	}
	if len(rt.his) != 1 {
		t.Fatalf("his has %d blocks, want 1", len(rt.his))
	}
	found := false
	for _, row := range rt.his[0].cells {
		for _, g := range row {
			if g.Char == 'm' {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("block grid missing command text")
	}
}