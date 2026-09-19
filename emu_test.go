package main

import (
	"strings"
	"testing"
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
		{"mode", func(b block) block {
			n := clone(b)
			gg := n.cells[0][0]
			n.cells[0][0] = vt10x.Glyph{Char: gg.Char, Mode: gg.Mode + 1, FG: gg.FG, BG: gg.BG}
			return n
		}, false},
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

func TestBlockText(t *testing.T) {
	cases := []struct {
		name string
		grid [][]vt10x.Glyph
		want string
	}{
		{
			"trailing blanks trimmed",
			func() [][]vt10x.Glyph {
				g := mkGrid(1, 5)
				set(g, 0, 0, 'a', 1, 2)
				set(g, 1, 0, 'b', 1, 2)
				return g
			}(),
			"ab",
		},
		{
			"all empty",
			func() [][]vt10x.Glyph {
				g := mkGrid(1, 4)
				set(g, 1, 0, ' ', 1, 2)
				return g
			}(),
			"",
		},
		{
			"nil grid",
			nil,
			"",
		},
		{
			"interior spaces preserved",
			func() [][]vt10x.Glyph {
				g := mkGrid(1, 4)
				set(g, 0, 0, 'a', 1, 2)
				set(g, 2, 0, 'c', 1, 2)
				return g
			}(),
			"a c",
		},
		{
			"multi row join",
			func() [][]vt10x.Glyph {
				g := mkGrid(2, 3)
				set(g, 0, 0, 'x', 1, 2)
				set(g, 0, 1, 'y', 1, 2)
				return g
			}(),
			"x\ny",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := blockText(c.grid); got != c.want {
				t.Fatalf("blockText = %q, want %q", got, c.want)
			}
		})
	}
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
		{"enter only", ansiEnterAltScreen, true},
		{"leave only", ansiLeaveAltScreen, false},
		{"enter then leave", ansiEnterAltScreen + ansiLeaveAltScreen, false},
		{"double enter then leave", ansiEnterAltScreen + ansiEnterAltScreen + ansiLeaveAltScreen, false},
		{"header then x unchanged", ansiAltPrefix + "x", false},
		{"trailing prefix guarded", ansiAltPrefix, false},
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
	cases := []struct {
		name    string
		data    string
		buffer  []byte
		first   bool
		inAlt   bool
		swallow bool
	}{
		{"idle pure ctrl-l", "\x0c", nil, false, false, true},
		{"double ctrl-l", "\x0c\x0c", nil, false, false, true},
		{"bootstrap prompt", "\x0c", nil, true, false, false},
		{"buffer non-empty", "\x0c", []byte("x"), false, false, false},
		{"in alt-screen", "\x0c", nil, false, true, false},
		{"pasted mixed", "\x0cA", nil, false, false, false},
		{"empty read", "", nil, false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := swallowCtrlL([]byte(c.data), c.buffer, c.first, c.inAlt); got != c.swallow {
				t.Fatalf("swallowCtrlL = %v, want %v", got, c.swallow)
			}
		})
	}
}

func TestComposeMarkerSplit(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(80, 24))
	var his []block
	boundary := func(code int) {
		g, _, _ := snapshotGrid(vt)
		if g != nil {
			his = append(his, newBlock(g, 80, code))
		}
		vt = vt10x.New(vt10x.WithSize(80, 24))
	}
	watched := [][]byte{ansiSepHead, []byte(ansiEnterAltScreen), []byte(ansiLeaveAltScreen)}
	var buffer []byte
	for chunk, err := range holdIter(&chunkReader{chunks: [][]byte{
		[]byte("..cmd\n" + string(ansiSepHead) + "0"),
		[]byte("\x07> "),
	}}, func(b []byte) int {
		return holdPrefixes(b, watched)
	}) {
		if len(chunk) > 0 {
			buffer = append(buffer, chunk...)
			buf := buffer
			buffer = nil
			if left := composeChunk(&vt, buf, boundary); len(left) > 0 {
				buffer = append(buffer, left...)
			}
		}
		if err != nil {
			break
		}
	}
	if len(his) != 1 {
		t.Fatalf("his has %d blocks, want 1", len(his))
	}
	found := false
	for _, row := range his[0].cells {
		for _, g := range row {
			if g.Char == 'm' {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("block grid missing command text")
	}
	// trailing prompt text after the marker must land in the fresh emulator,
	// not the frozen block (regression: stale vt passed by value)
	for _, row := range his[0].cells {
		for _, g := range row {
			if g.Char == '>' {
				t.Fatal("trailing prompt text leaked into the frozen block")
			}
		}
	}
	foundPrompt := false
	cols, rows := vt.Size()
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			if vt.Cell(x, y).Char == '>' {
				foundPrompt = true
			}
		}
	}
	if !foundPrompt {
		t.Fatal("trailing prompt text missing from live emulator")
	}
}
