package main

import (
	"github.com/hinshun/vt10x"
)

type Block struct {
	Cells [][]vt10x.Glyph
	Width int
}

// archiveRows bounds the vertical extent of the tall replays used to
// finalize a block. Output exceeding this many lines is truncated
// (scrolled out of the archive grid).
const archiveRows = 16384
