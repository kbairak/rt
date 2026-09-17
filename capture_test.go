package main

import (
	"strings"
	"testing"

	"github.com/hinshun/vt10x"
)

func mkParser() *blockParser {
	return newBlockParser(&BlockStore{}, 40)
}

func gridContains(g [][]vt10x.Glyph, want string) bool {
	for _, row := range g {
		if strings.Contains(cellText(row), want) {
			return true
		}
	}
	return false
}

func TestBlockParserSingle(t *testing.T) {
	p := mkParser()
	p.Feed([]byte("startup noiseRTMRK%# ls\nfile1  file2\nRTMRK%# "))

	blocks := p.store.All()
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if !gridContains(blocks[0].Cells, "file1") {
		t.Fatalf("expected output containing 'file1', got %v", blocks[0].Cells)
	}
}

func TestBlockParserMultiple(t *testing.T) {
	p := mkParser()
	p.Feed([]byte("RTMRK%# ls\nfile1\nRTMRK%# pwd\n/home/user\nRTMRK%# "))

	blocks := p.store.All()
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
	if !gridContains(blocks[0].Cells, "file1") {
		t.Fatalf("block 0 should hold ls output: %v", blocks[0].Cells)
	}
	if !gridContains(blocks[1].Cells, "/home/user") {
		t.Fatalf("block 1 should hold pwd output: %v", blocks[1].Cells)
	}
}

func TestBlockParserSplitChunks(t *testing.T) {
	p := mkParser()
	p.Feed([]byte("garbageRTMRK%# ls\nhel"))
	p.Feed([]byte("lo\nRTMRK%# "))

	blocks := p.store.All()
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if !gridContains(blocks[0].Cells, "hello") {
		t.Fatalf("expected output containing 'hello', got %v", blocks[0].Cells)
	}
}

func TestBlockParserSkipsInitialNoise(t *testing.T) {
	p := mkParser()
	p.Feed([]byte("some startup noise from shell RTMRK%# ls\noutput\nRTMRK%# "))

	blocks := p.store.All()
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if !gridContains(blocks[0].Cells, "output") {
		t.Fatalf("expected block output: %v", blocks[0].Cells)
	}
}

// Regression: block must retain full output even when it exceeds a real
// terminal's height (tall-archive replay must not lose scrolled lines).
func TestBlockParserPreservesLongOutput(t *testing.T) {
	p := mkParser()
	var sb strings.Builder
	sb.WriteString("RTMRK%# seq\n")
	for i := 0; i < 80; i++ {
		sb.WriteString("line-")
		sb.WriteString(itoa(i))
		sb.WriteString("\n")
	}
	sb.WriteString("RTMRK%# ")
	p.Feed([]byte(sb.String()))

	blocks := p.store.All()
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if !gridContains(blocks[0].Cells, "line-79") {
		t.Fatalf("expected last output line preserved, rows=%d", len(blocks[0].Cells))
	}
}

func TestBlockParserBlanksMarker(t *testing.T) {
	p := mkParser()
	p.Feed([]byte("RTMRK%# ls\nx\nRTMRK%# "))

	blocks := p.store.All()
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if gridContains(blocks[0].Cells, "RTMRK") {
		t.Fatalf("marker not blanked: %v", blocks[0].Cells)
	}
}

// The live emulator's screen must be cleared at each command boundary so
// the live region shows only the current prompt, not the whole session.
func TestLiveClearedAtBoundary(t *testing.T) {
	p := mkParser()
	p.Feed([]byte("RTMRK%# ls\nfile1\nRTMRK%# "))

	blocks := p.store.All()
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if !gridContains(blocks[0].Cells, "file1") {
		t.Fatalf("block should hold command output, got %v", blocks[0].Cells)
	}

	live, _, _ := snapshotGrid(p.Live())
	if gridContains(live, "file1") {
		t.Fatalf("live grid still holds finished command output: %v", live)
	}
	top := 0
	for top < len(live) && rowEmpty(live[top]) {
		top++
	}
	live = live[top:]
	if len(live) != 1 {
		t.Fatalf("expected live grid of just the prompt, got %d rows", len(live))
	}
}

func TestStripMarkerRow(t *testing.T) {
	row := make([]vt10x.Glyph, 16)
	// paint "RTMRK% exit" into the row, rest spaces
	s := "RTMRK% exit"
	for i := 0; i < len(s); i++ {
		row[i] = vt10x.Glyph{Char: rune(s[i]), Mode: 0, FG: vt10x.DefaultFG, BG: vt10x.DefaultBG}
	}
	for i := len(s); i < 16; i++ {
		row[i] = vt10x.Glyph{Char: ' ', Mode: 0, FG: vt10x.DefaultFG, BG: vt10x.DefaultBG}
	}
	out, stripped := stripMarker(row)
	if !stripped {
		t.Fatalf("expected marker found, none stripped")
	}
	text := cellText(out)
	if !strings.HasPrefix(text, "% exit") {
		t.Fatalf("expected marker stripped, got %q", text)
	}
}
