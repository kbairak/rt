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