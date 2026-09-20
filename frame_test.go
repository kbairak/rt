package main

import (
	"strings"
	"testing"

	"github.com/hinshun/vt10x"
)

// vtWith builds a sized emulator and feeds it data.
func vtWith(t *testing.T, cols, rows int, data string) vt10x.Terminal {
	t.Helper()
	vt := vt10x.New(vt10x.WithSize(cols, rows))
	if _, err := vt.Write([]byte(data)); err != nil {
		t.Fatalf("vt.Write: %v", err)
	}
	return vt
}

func count(s, sub string) int {
	return strings.Count(s, sub)
}

func TestFrameInitialFullClear(t *testing.T) {
	r := &renderer{}
	out := string(r.frame(nil, vtWith(t, 80, 24, "hello"), 80, 24))

	if !strings.Contains(out, ansiClearScreen) {
		t.Fatal("first frame must clear the screen")
	}
	if !strings.HasPrefix(out, ansiSyncBegin) || !strings.HasSuffix(out, ansiSyncEnd) {
		t.Fatal("frame must be wrapped in synchronized update")
	}
	if !strings.Contains(out, ansiCursorPos(0, 0)) {
		t.Fatal("first frame must position row 0")
	}
}

func TestFrameUnchangedEmitsNoRows(t *testing.T) {
	vt := vtWith(t, 80, 24, "hello")
	r := &renderer{}
	r.frame(nil, vt, 80, 24)

	out := string(r.frame(nil, vt, 80, 24))
	if strings.Contains(out, ansiClearScreen) {
		t.Fatal("unchanged frame must not clear")
	}
	if strings.Contains(out, ansiCursorPos(0, 0)) {
		t.Fatal("unchanged frame must not re-emit row 0")
	}
}

func TestFrameEmitsOnlyChangedRow(t *testing.T) {
	vt := vtWith(t, 80, 24, "hello")
	r := &renderer{}
	r.frame(nil, vt, 80, 24)

	if _, err := vt.Write([]byte("\r\nworld")); err != nil {
		t.Fatalf("vt.Write: %v", err)
	}
	out := string(r.frame(nil, vt, 80, 24))

	if strings.Contains(out, ansiCursorPos(0, 0)) {
		t.Fatal("unchanged row 0 must not be re-emitted")
	}
	if !strings.Contains(out, ansiCursorPos(1, 0)) {
		t.Fatal("changed row 1 must be emitted")
	}
	if strings.Contains(out, ansiCursorPos(2, 0)) {
		t.Fatal("row past content must not be emitted")
	}
}

func TestFrameClearsStaleRows(t *testing.T) {
	g := mkGrid(3, 80)
	for y := 0; y < 3; y++ {
		set(g, 0, y, 'x', vt10x.DefaultFG, vt10x.DefaultBG)
	}
	his := []block{newBlock(g, 80, 0)}

	vt := vtWith(t, 80, 24, "")
	r := &renderer{}
	r.frame(his, vt, 80, 24)

	out := string(r.frame(nil, vt, 80, 24))
	if strings.Contains(out, ansiClearScreen) {
		t.Fatal("shrink must not full-clear")
	}
	for y := 0; y < 3; y++ {
		if !strings.Contains(out, ansiCursorPos(y, 0)) {
			t.Fatalf("stale row %d must be erased", y)
		}
	}
	if count(out, ansiClearLine) < 3 {
		t.Fatal("stale rows must be erased with clear-line")
	}
}

func TestFrameResizeForcesFullClear(t *testing.T) {
	vt := vtWith(t, 80, 24, "hello")
	r := &renderer{}
	r.frame(nil, vt, 80, 24)

	out := string(r.frame(nil, vt, 40, 24))
	if !strings.Contains(out, ansiClearScreen) {
		t.Fatal("size change must force a full clear")
	}
}
