package main

import (
	"strings"
	"testing"

	"github.com/hinshun/vt10x"
)

// mkBlock builds a one-row block whose only cell is c.
func mkBlock(c rune, width int) block {
	g := mkGrid(1, width)
	set(g, 0, 0, c, vt10x.DefaultFG, vt10x.DefaultBG)
	return newBlock(g, width, 0)
}

func actsEqual(a, b []overlayAction) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDecodeOverlayKeys(t *testing.T) {
	cases := []struct {
		in   string
		want []overlayAction
	}{
		{"j", []overlayAction{ovOlder}},
		{"k", []overlayAction{ovNewer}},
		{"g", []overlayAction{ovNewest}},
		{"G", []overlayAction{ovOldest}},
		{"\r", []overlayAction{ovCopy}},
		{"\n", []overlayAction{ovCopy}},
		{"y", []overlayAction{ovCopy}},
		{"q", []overlayAction{ovCancel}},
		{"\x1b", []overlayAction{ovCancel}},
		{"\x0e", []overlayAction{ovOlder}},
		{"\x10", []overlayAction{ovNewer}},
		{"\x1e", []overlayAction{ovCancel}},
		{"z", nil},
		{"?", nil},
		{"h", nil},
		{"jk", []overlayAction{ovOlder, ovNewer}},
	}
	for _, c := range cases {
		acts, pend := decodeOverlay(nil, []byte(c.in))
		if len(pend) != 0 {
			t.Fatalf("%q: unexpected pending %q", c.in, pend)
		}
		if !actsEqual(acts, c.want) {
			t.Fatalf("%q: got %v want %v", c.in, acts, c.want)
		}
	}
}

func TestDecodeOverlayEscapes(t *testing.T) {
	cases := []struct {
		in   string
		want overlayAction
	}{
		{"\x1b[A", ovNewer},
		{"\x1b[B", ovOlder},
		{"\x1b[H", ovNewest},
		{"\x1b[F", ovOldest},
		{"\x1b[1~", ovNewest},
		{"\x1b[4~", ovOldest},
		{"\x1bOH", ovNewest},
		{"\x1bOF", ovOldest},
	}
	for _, c := range cases {
		acts, pend := decodeOverlay(nil, []byte(c.in))
		if len(pend) != 0 {
			t.Fatalf("%q: unexpected pending %q", c.in, pend)
		}
		if !actsEqual(acts, []overlayAction{c.want}) {
			t.Fatalf("%q: got %v want %v", c.in, acts, c.want)
		}
	}
}

func TestDecodeOverlaySplitSequence(t *testing.T) {
	acts, pend := decodeOverlay(nil, []byte("\x1b["))
	if len(acts) != 0 || string(pend) != "\x1b[" {
		t.Fatalf("split head: acts=%v pend=%q", acts, pend)
	}
	acts, pend = decodeOverlay(pend, []byte("A"))
	if !actsEqual(acts, []overlayAction{ovNewer}) || len(pend) != 0 {
		t.Fatalf("split tail: acts=%v pend=%q", acts, pend)
	}
}

func TestCopyNavigation(t *testing.T) {
	// history is already collapsed at append time: A B C (oldest -> newest).
	h := []block{mkBlock('A', 4), mkBlock('B', 4), mkBlock('C', 4)}
	s := &session{history: h, copyActive: true, sel: len(h) - 1}

	s.handleCopyInput([]byte("j")) // C -> B
	if s.sel != 1 {
		t.Fatalf("j: sel=%d want 1", s.sel)
	}
	s.handleCopyInput([]byte("j")) // B -> A
	if s.sel != 0 {
		t.Fatalf("j: sel=%d want 0", s.sel)
	}
	s.handleCopyInput([]byte("j")) // oldest: no move
	if s.sel != 0 {
		t.Fatalf("j at oldest: sel=%d want 0", s.sel)
	}
	s.handleCopyInput([]byte("k")) // A -> B
	if s.sel != 1 {
		t.Fatalf("k: sel=%d want 1", s.sel)
	}
	s.handleCopyInput([]byte("G")) // oldest
	if s.sel != 0 {
		t.Fatalf("G: sel=%d want 0", s.sel)
	}
	s.handleCopyInput([]byte("g")) // newest
	if s.sel != 2 {
		t.Fatalf("g: sel=%d want 2", s.sel)
	}
}

func TestAppendBlockCollapsesIdentical(t *testing.T) {
	s := &session{}
	if !s.appendBlock(mkBlock('A', 4)) {
		t.Fatal("first append must create an entry")
	}
	if s.appendBlock(mkBlock('A', 4)) {
		t.Fatal("identical append must collapse")
	}
	if s.appendBlock(mkBlock('A', 4)) {
		t.Fatal("identical append must collapse")
	}
	if !s.appendBlock(mkBlock('B', 4)) {
		t.Fatal("different append must create an entry")
	}
	if len(s.history) != 2 {
		t.Fatalf("entries=%d want 2", len(s.history))
	}
	if s.history[0].count != 3 {
		t.Fatalf("collapsed count=%d want 3", s.history[0].count)
	}
	if s.history[1].count != 1 {
		t.Fatalf("new entry count=%d want 1", s.history[1].count)
	}
}

func TestCopyExitActions(t *testing.T) {
	s := &session{history: []block{mkBlock('A', 4)}, copyActive: true, sel: 0}

	s.handleCopyInput([]byte("y"))
	if s.copyActive || !s.copyPending || s.copySel != 0 {
		t.Fatalf("copy: active=%v pending=%v sel=%d", s.copyActive, s.copyPending, s.copySel)
	}
	s.copyPending = false
	s.copyActive = true
	s.handleCopyInput([]byte("q"))
	if s.copyActive || s.copyPending {
		t.Fatalf("cancel: active=%v pending=%v", s.copyActive, s.copyPending)
	}
}

func TestOverlayComposeShiftsBlocksAndMarksSelection(t *testing.T) {
	his := []block{mkBlock('A', 10)}
	vt := vtWith(t, 10, 6, "LIVE")
	rows, _, _ := compose(his, vt, 10, 6, &overlay{sel: 0})

	all := ""
	for _, r := range rows {
		all += string(r) + "\n"
	}
	if strings.Contains(all, "LIVE") {
		t.Fatal("live grid must be hidden in copy mode")
	}
	if !strings.Contains(all, "COPY 1/1") {
		t.Fatalf("status row missing: %q", all)
	}
	if !strings.Contains(all, ansiReverse) {
		t.Fatal("status row must be reverse-videoed")
	}
	if !strings.Contains(all, ansiGreen+gutterLine+ansiReset) {
		t.Fatalf("selected block must have a green gutter line: %q", all)
	}
	// The block content is shifted one column right, behind the gutter.
	if !strings.HasPrefix(string(rows[1]), ansiGreen+gutterLine+ansiReset) {
		t.Fatalf("selected block row must start with the gutter: %q", rows[1])
	}
}

func TestOverlayCropsFullWidthLines(t *testing.T) {
	g := mkGrid(1, 4)
	for x := 0; x < 4; x++ {
		set(g, x, 0, rune('a'+x), vt10x.DefaultFG, vt10x.DefaultBG)
	}
	his := []block{newBlock(g, 4, 0)}

	rows, _, _ := compose(his, vtWith(t, 4, 4, ""), 4, 4, &overlay{sel: 0})
	row := string(rows[1])
	if strings.ContainsRune(row, 'd') {
		t.Fatalf("4-wide line must be cropped to 3 content columns: %q", row)
	}
	if !strings.ContainsRune(row, 'c') {
		t.Fatalf("first three columns must survive: %q", row)
	}
}

func TestClipboardCommandsPerGOOS(t *testing.T) {
	if got := clipboardCommands("darwin"); len(got) != 1 || got[0][0] != "pbcopy" {
		t.Fatalf("darwin: %v", got)
	}
	linux := clipboardCommands("linux")
	if len(linux) != 2 || linux[0][0] != "wl-copy" || linux[1][0] != "xclip" {
		t.Fatalf("linux: %v", linux)
	}
	if got := clipboardCommands("plan9"); got != nil {
		t.Fatalf("plan9: %v", got)
	}
}

func TestOnlyHasCopyKeys(t *testing.T) {
	if !onlyHasCopyKeys([]byte{copyKey}) || !onlyHasCopyKeys([]byte{copyKey, copyKey}) {
		t.Fatal("pure copy-key reads must match")
	}
	if onlyHasCopyKeys(nil) || onlyHasCopyKeys([]byte{copyKey, 'a'}) {
		t.Fatal("empty or mixed reads must not match")
	}
}
