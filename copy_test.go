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
		{"d", []overlayAction{ovPageDown}},
		{"u", []overlayAction{ovPageUp}},
		{"z", nil},
		{"x", []overlayAction{ovDelete}},
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
	s := &session{history: h, view: h, copyActive: true, sel: len(h) - 1, height: 10}

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
	s := &session{history: []block{mkBlock('A', 4)}, view: []block{mkBlock('A', 4)}, copyActive: true, sel: 0}

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

// mkRows builds a block of one-column rows, one glyph per row.
func mkRows(chars string, width int) block {
	g := mkGrid(len(chars), width)
	for y, c := range chars {
		set(g, 0, y, c, vt10x.DefaultFG, vt10x.DefaultBG)
	}
	return newBlock(g, width, 0)
}

func TestAdjustScrollRevealsMinimally(t *testing.T) {
	// Three 2-line entries (separator + 1 row), viewport 3 lines.
	his := []block{mkBlock('A', 4), mkBlock('B', 4), mkBlock('C', 4)}
	// top lines: C=0, B=2, A=4; total=6.
	const v = 3

	// Newest fully visible from scroll 2? It occupies lines 0..1, so scroll=2
	// hides it; reveal minimally -> 0.
	if got := adjustScroll(his, 2, 2, v, false); got != 0 {
		t.Fatalf("select newest: scroll=%d want 0", got)
	}
	// Middle occupies 2..3; from scroll 0 reveal bottom -> 1.
	if got := adjustScroll(his, 1, 0, v, false); got != 1 {
		t.Fatalf("select middle: scroll=%d want 1", got)
	}
	// Middle already fully visible at scroll 1 -> unchanged.
	if got := adjustScroll(his, 1, 1, v, false); got != 1 {
		t.Fatalf("middle visible: scroll=%d want 1", got)
	}
	// Oldest occupies 4..5; clamp at max=total-v=3.
	if got := adjustScroll(his, 0, 0, v, false); got != 3 {
		t.Fatalf("select oldest: scroll=%d want 3", got)
	}
}

func TestAdjustScrollAlignsTallBlockToTop(t *testing.T) {
	tall := mkRows("abcde", 4)            // 5 rows + separator = 6 lines, viewport 3
	his := []block{tall, mkBlock('Z', 4)} // Z newest (2 lines), tall oldest
	// tall top line = 2. h=6 > v=3 -> align top.
	if got := adjustScroll(his, 0, 0, 3, false); got != 2 {
		t.Fatalf("tall block: scroll=%d want 2", got)
	}
}

func TestComposeScrollCropsTop(t *testing.T) {
	// A older, B newer; each has 2 rows (separator + 2 = 3 lines).
	a := mkRows("pq", 4)
	b := mkRows("xy", 4)
	his := []block{a, b} // B lines 0..2, A lines 3..5

	// scroll=2 lands inside B: skip separator and row "x", start at "y".
	rows, _, _ := compose(his, vtWith(t, 10, 4, ""), 10, 4, &overlay{sel: 0, scroll: 2})
	if !strings.Contains(string(rows[0]), "y") || strings.Contains(string(rows[0]), "x") {
		t.Fatalf("row 0 must start mid-block at 'y': %q", rows[0])
	}
	// Next row is A's separator, then A's first row.
	if !strings.Contains(string(rows[1]), "─") {
		t.Fatalf("row 1 must be A's separator: %q", rows[1])
	}
	if !strings.Contains(string(rows[2]), "p") {
		t.Fatalf("row 2 must be A's first row: %q", rows[2])
	}
}

func TestPageReanchorsSelection(t *testing.T) {
	// Four 2-line entries; top lines D=0 C=2 B=4 A=6; total=8. V=4, step=2.
	his := []block{mkBlock('A', 4), mkBlock('B', 4), mkBlock('C', 4), mkBlock('D', 4)}
	s := &session{history: his, view: his, copyActive: true, sel: 3, scroll: 0, height: 5}

	s.handleCopyInput([]byte{'d'}) // page down -> C at window top
	if s.scroll != 2 || s.sel != 2 {
		t.Fatalf("page down: scroll=%d sel=%d want 2,2", s.scroll, s.sel)
	}
	s.handleCopyInput([]byte{'d'}) // -> B
	if s.scroll != 4 || s.sel != 1 {
		t.Fatalf("page down: scroll=%d sel=%d want 4,1", s.scroll, s.sel)
	}
	s.handleCopyInput([]byte{'d'}) // clamped at max=4, B still fully visible
	if s.scroll != 4 || s.sel != 1 {
		t.Fatalf("page down clamp: scroll=%d sel=%d want 4,1", s.scroll, s.sel)
	}
	s.handleCopyInput([]byte{'u'}) // up to 2; B still fully visible -> keep
	if s.scroll != 2 || s.sel != 1 {
		t.Fatalf("page up visible: scroll=%d sel=%d want 2,1", s.scroll, s.sel)
	}
	s.handleCopyInput([]byte{'u'}) // up to 0; B out -> top block D
	if s.scroll != 0 || s.sel != 3 {
		t.Fatalf("page up: scroll=%d sel=%d want 0,3", s.scroll, s.sel)
	}
}

func TestPageSelectsBlockFillingWindow(t *testing.T) {
	// tall: 6 rows (7 lines); Z: 2 lines. top lines Z=0, tall=2; total=9.
	tall := mkRows("abcde", 4)
	his := []block{tall, mkBlock('Z', 4)}
	s := &session{history: his, view: his, copyActive: true, sel: 1, scroll: 1, height: 5}

	s.handleCopyInput([]byte{'d'}) // scroll=3, window inside tall, no block start
	if s.scroll != 3 || s.sel != 0 {
		t.Fatalf("fill window: scroll=%d sel=%d want 3,0", s.scroll, s.sel)
	}
}

func TestDeleteSelectedBlock(t *testing.T) {
	his := []block{mkBlock('A', 4), mkBlock('B', 4), mkBlock('C', 4)}
	s := &session{history: his, view: his, copyActive: true, sel: 1, height: 5}

	s.handleCopyInput([]byte{'x'})
	if len(s.history) != 2 || s.history[0].hash != his[0].hash || s.history[1].hash != his[2].hash {
		t.Fatalf("history after delete: %v", s.history)
	}
	if s.sel != 1 || len(s.view) != 2 {
		t.Fatalf("sel=%d view=%d want 1,2", s.sel, len(s.view))
	}

	s.sel = 1
	s.handleCopyInput([]byte{'x'})
	if len(s.history) != 1 || s.sel != 0 {
		t.Fatalf("second delete: history=%d sel=%d want 1,0", len(s.history), s.sel)
	}

	s.handleCopyInput([]byte{'x'})
	if len(s.history) != 0 || s.sel != 0 || s.scroll != 0 {
		t.Fatalf("empty delete: history=%d sel=%d scroll=%d", len(s.history), s.sel, s.scroll)
	}
}

func TestDeleteUnderFilter(t *testing.T) {
	his := []block{mkBlock('A', 4), mkBlock('B', 4)}
	s := &session{
		history: his, view: filterHistory(his, "B"), filter: "B",
		copyActive: true, sel: 0, height: 5,
	}
	s.handleCopyInput([]byte{'x'})
	if len(s.history) != 1 || s.history[0].hash != his[0].hash {
		t.Fatalf("history after filtered delete: %v", s.history)
	}
	if len(s.view) != 0 || s.sel != 0 {
		t.Fatalf("view=%d sel=%d want 0,0", len(s.view), s.sel)
	}
}

func searchKinds(evs []searchEvent) []searchEventKind {
	out := make([]searchEventKind, len(evs))
	for i, e := range evs {
		out[i] = e.kind
	}
	return out
}

func kindsEqual(a, b []searchEventKind) bool {
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

func TestDecodeSearch(t *testing.T) {
	evs, pend := decodeSearch(nil, []byte("ab\x7f"))
	if !kindsEqual(searchKinds(evs), []searchEventKind{seRune, seRune, seBackspace}) || len(pend) != 0 {
		t.Fatalf("basic: evs=%v pend=%q", evs, pend)
	}
	if evs[0].r != 'a' || evs[1].r != 'b' {
		t.Fatalf("runes: %v", evs)
	}

	evs, _ = decodeSearch(nil, []byte("\r"))
	if !kindsEqual(searchKinds(evs), []searchEventKind{seApply}) {
		t.Fatalf("enter: %v", evs)
	}
	evs, _ = decodeSearch(nil, []byte("\x1b"))
	if !kindsEqual(searchKinds(evs), []searchEventKind{seCancel}) {
		t.Fatalf("esc: %v", evs)
	}

	// Control bytes ignored; a CSI sequence is consumed, not treated as Esc.
	evs, pend = decodeSearch(nil, []byte("\x01\x1b[A\x02"))
	if len(evs) != 0 || len(pend) != 0 {
		t.Fatalf("ignored: evs=%v pend=%q", evs, pend)
	}

	// Split escape sequence is held, then consumed.
	evs, pend = decodeSearch(nil, []byte("\x1b["))
	if len(evs) != 0 || string(pend) != "\x1b[" {
		t.Fatalf("split head: evs=%v pend=%q", evs, pend)
	}
	evs, pend = decodeSearch(pend, []byte("A"))
	if len(evs) != 0 || len(pend) != 0 {
		t.Fatalf("split tail: evs=%v pend=%q", evs, pend)
	}

	evs, _ = decodeSearch(nil, []byte("\x17"))
	if !kindsEqual(searchKinds(evs), []searchEventKind{seKillWord}) {
		t.Fatalf("ctrl-w: %v", evs)
	}
}

func TestKillWord(t *testing.T) {
	cases := []struct{ in, want string }{
		{"foo bar", "foo "},
		{"foo ", ""},
		{"foo   ", ""},
		{"foo", ""},
		{"", ""},
		{"  ", ""},
		{"a b  c", "a b  "},
	}
	for _, c := range cases {
		if got := killWord(c.in); got != c.want {
			t.Fatalf("killWord(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// mkText builds a one-row block from a string.
func mkText(s string, width int) block {
	g := mkGrid(1, width)
	for x, c := range s {
		set(g, x, 0, c, vt10x.DefaultFG, vt10x.DefaultBG)
	}
	return newBlock(g, width, 0)
}

func TestFilterHistory(t *testing.T) {
	his := []block{mkText("hello", 8), mkText("world", 8)}
	if got := filterHistory(his, "ell"); len(got) != 1 || string(got[0].cells[0][0].Char) != "h" {
		t.Fatalf("substring: %v", got)
	}
	if got := filterHistory(his, "WORLD"); len(got) != 0 {
		t.Fatalf("case sensitive: %v", got)
	}
	if got := filterHistory(his, ""); len(got) != len(his) {
		t.Fatalf("empty query must return all: %v", got)
	}
	if got := filterHistory(his, "nope"); len(got) != 0 {
		t.Fatalf("no match: %v", got)
	}
}

func TestSearchApplyAndCancel(t *testing.T) {
	his := []block{mkText("hello", 8), mkText("world", 8)}
	s := &session{history: his, view: his, copyActive: true, searchActive: true, sel: 1, height: 10}

	s.handleSearchInput([]byte("ell"))
	if s.query != "ell" || !s.searchActive {
		t.Fatalf("typing: query=%q active=%v", s.query, s.searchActive)
	}
	s.handleSearchInput([]byte("\r"))
	if s.searchActive || s.filter != "ell" || len(s.view) != 1 || s.sel != 0 {
		t.Fatalf("apply: active=%v filter=%q view=%d sel=%d", s.searchActive, s.filter, len(s.view), s.sel)
	}

	// Re-enter search and cancel: filter cleared, full view restored.
	s.searchActive = true
	s.query = ""
	s.handleSearchInput([]byte("\x1b"))
	if s.searchActive || s.filter != "" || len(s.view) != len(his) || s.sel != len(his)-1 {
		t.Fatalf("cancel: active=%v filter=%q view=%d sel=%d", s.searchActive, s.filter, len(s.view), s.sel)
	}
}

func TestSearchKillWord(t *testing.T) {
	his := []block{mkText("hello world", 16)}
	s := &session{history: his, view: his, copyActive: true, searchActive: true, height: 10}

	s.handleSearchInput([]byte("foo bar"))
	if s.query != "foo bar" {
		t.Fatalf("typing: query=%q", s.query)
	}
	s.handleSearchInput([]byte("\x17"))
	if s.query != "foo " {
		t.Fatalf("first ^w: query=%q", s.query)
	}
	s.handleSearchInput([]byte("\x17"))
	if s.query != "" {
		t.Fatalf("second ^w: query=%q", s.query)
	}
}

func TestSearchNoMatchStaysInSearch(t *testing.T) {
	his := []block{mkText("hello", 8)}
	s := &session{history: his, view: his, copyActive: true, searchActive: true, sel: 0, height: 10}

	s.handleSearchInput([]byte("zzz\r"))
	if !s.searchActive || s.query != "zzz" || s.filter != "" {
		t.Fatalf("no match: active=%v query=%q filter=%q", s.searchActive, s.query, s.filter)
	}
}

func TestOverlayStatusSearch(t *testing.T) {
	st := overlayStatus(&overlay{searching: true, query: "foo"}, 3, 80)
	if !strings.Contains(st, "SEARCH foo") {
		t.Fatalf("search status: %q", st)
	}
	st = overlayStatus(&overlay{sel: 0, filter: "foo"}, 3, 80)
	if !strings.Contains(st, "COPY 3/3") || !strings.Contains(st, "/foo") {
		t.Fatalf("filter status: %q", st)
	}
}

func TestEntryHeightCollapsed(t *testing.T) {
	b := mkRows("abcdefghij", 8) // 10 content rows
	if got := entryHeight(b, false); got != 11 {
		t.Fatalf("expanded height=%d want 11", got)
	}
	if got := entryHeight(b, true); got != 9 { // sep + 7 rows + indicator
		t.Fatalf("collapsed height=%d want 9", got)
	}
	short := mkRows("abc", 8)
	if entryHeight(short, true) != entryHeight(short, false) {
		t.Fatal("short block must be unchanged by collapse")
	}
}

func TestToggleCollapse(t *testing.T) {
	his := []block{mkRows("abcdefghij", 8)}
	s := &session{history: his, view: his, copyActive: true, sel: 0, height: 12}

	s.handleCopyInput([]byte("c"))
	if !s.collapsed {
		t.Fatal("c must collapse")
	}
	s.handleCopyInput([]byte("c"))
	if s.collapsed {
		t.Fatal("c must expand")
	}
}

func TestCollapseKeyIsLiteralInSearch(t *testing.T) {
	his := []block{mkRows("abc", 8)}
	s := &session{history: his, view: his, copyActive: true, searchActive: true, height: 12}

	s.handleSearchInput([]byte("c"))
	if s.collapsed || s.query != "c" {
		t.Fatalf("search: collapsed=%v query=%q", s.collapsed, s.query)
	}
}

func TestComposeCollapsedShowsIndicator(t *testing.T) {
	his := []block{mkRows("abcdefghij", 8)}

	rows, _, _ := compose(his, vtWith(t, 20, 12, ""), 20, 12, &overlay{sel: 0, collapsed: true})
	all := ""
	for _, r := range rows {
		all += string(r) + "\n"
	}
	if !strings.Contains(string(rows[8]), "3 more lines") {
		t.Fatalf("row 8 must be the indicator: %q", rows[8])
	}
	if strings.Contains(all, "h") {
		t.Fatal("rows past the cap must not be drawn")
	}

	rows, _, _ = compose(his, vtWith(t, 20, 12, ""), 20, 12, &overlay{sel: 0})
	if !strings.Contains(string(rows[10]), "j") || strings.Contains(string(rows[10]), "more") {
		t.Fatalf("expanded last row: %q", rows[10])
	}
}

func TestFilterMatchesHiddenLines(t *testing.T) {
	his := []block{mkRows("abcdefghij", 8)}
	if got := filterHistory(his, "j"); len(got) != 1 {
		t.Fatalf("filter on collapsed-away line must match: %d", len(got))
	}
}

func TestDecodeOverlayReplay(t *testing.T) {
	acts, _ := decodeOverlay(nil, []byte("r"))
	if !actsEqual(acts, []overlayAction{ovReplay}) {
		t.Fatalf("r: %v", acts)
	}
}

func TestAppendBlockCollapseKeepsNewestTx(t *testing.T) {
	b1 := mkBlock('A', 4)
	b1.tx = []byte("one\r")
	b2 := mkBlock('A', 4)
	b2.tx = []byte("two\r")
	s := &session{}
	s.appendBlock(b1)
	s.appendBlock(b2)
	if len(s.history) != 1 || s.history[0].count != 2 {
		t.Fatalf("entries=%d count=%d", len(s.history), s.history[0].count)
	}
	if got := string(s.history[0].tx); got != "two\r" {
		t.Fatalf("tx=%q want newest", got)
	}
}

func TestReplayAction(t *testing.T) {
	b := mkBlock('A', 4)
	b.tx = []byte("ls\r")
	s := &session{history: []block{b}, view: []block{b}, copyActive: true, sel: 0, height: 10}

	s.handleCopyInput([]byte("r"))
	if s.copyActive || !s.replayPending {
		t.Fatalf("active=%v pending=%v", s.copyActive, s.replayPending)
	}
	if got := string(s.replayTx); got != "ls\r" {
		t.Fatalf("replayTx=%q want ls\\r", got)
	}
}
