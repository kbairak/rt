package main

import (
	"os"
	"path/filepath"
)

type rcScript struct {
	path    string
	cleanup func()
}

// writeRcScript creates a temp rc file that makes sh print the separator as
// the leading part of every prompt. sh is launched with ENV pointing here,
// so this is its only configuration.
func writeRcScript() (rcScript, error) {
	dir, err := os.MkdirTemp("", "rt-*")
	if err != nil {
		return rcScript{}, err
	}
	path := filepath.Join(dir, "rc")
	// $'...' ANSI-C quoting embeds literal ESC / BEL in PS1.
	hook := "PS1=$'\\x1b]" + sepPayload + "\\x07$ '\nPS2='> '\n"
	if werr := os.WriteFile(path, []byte(hook), 0o600); werr != nil {
		_ = os.RemoveAll(dir)
		return rcScript{}, werr
	}
	return rcScript{path: path, cleanup: func() { _ = os.RemoveAll(dir) }}, nil
}
