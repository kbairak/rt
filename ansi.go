package main

import "strings"

// ANSI escape sequences used by the render and capture paths. ESC starts a
// control sequence; CSI (Control Sequence Introducer) is ESC [ followed by
// parameters and a final command byte.
const (
	_esc = "\x1b"
	_csi = _esc + "["

	// ED: erase display (cursor position is preserved)
	AnsiED = _csi + "2J"
	// CUP: move cursor to home position (row 1, col 1)
	AnsiHome = _csi + "H"

	AnsiShowCursor = _csi + "?25h"
	AnsiHideCursor = _csi + "?25l"

	// Alternate screen buffer
	AnsiAltOn  = _csi + "?1049h"
	AnsiAltOff = _csi + "?1049l"

	// Bracketed paste off
	AnsiBracketOff = _csi + "?2004l"

	// DECSTBM reset: scroll region back to the full screen
	AnsiResetScroll = _csi + "r"

	// SGR: reset all attributes
	AnsiSGRReset = _csi + "0m"
)

// cursorPos builds a CUP sequence positioning the cursor at the given
// 0-based row and column.
func cursorPos(row, col int) string {
	return _csi + itoa(row+1) + ";" + itoa(col+1) + "H"
}

// sgrSeq builds an SGR sequence from semicolon-joined attribute codes.
func sgrSeq(params []string) string {
	return _csi + strings.Join(params, ";") + "m"
}
