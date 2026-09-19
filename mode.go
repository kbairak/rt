package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v2"
)

// resolveShell picks the shell to run: an explicit positional argument wins,
// then $SHELL, then "sh".
func resolveShell(c *cli.Context) string {
	if arg := c.Args().First(); arg != "" {
		return arg
	}
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "sh"
}

// detectMode infers the shell mode from the shell path: the basename with a
// leading '-' (login shell) stripped, matched against the supported modes.
func detectMode(shell string) (string, error) {
	base := strings.TrimPrefix(filepath.Base(shell), "-")
	switch {
	case base == "zsh":
		return modeZsh, nil
	case base == "bash":
		return modeBash, nil
	case base == "sh":
		return modeSh, nil
	case base == "python" || base == "python3" || strings.HasPrefix(base, "python3."):
		return modePython, nil
	default:
		return "", fmt.Errorf("unsupported shell %q: use --mode sh|zsh|bash|python", shell)
	}
}

// resolveMode validates an explicit --mode, otherwise infers it from the shell.
func resolveMode(c *cli.Context) (string, error) {
	if flagMode := c.String("mode"); flagMode != "" {
		switch flagMode {
		case modeSh, modeZsh, modeBash, modePython:
			return flagMode, nil
		default:
			return "", fmt.Errorf("unsupported mode %q: use --mode sh|zsh|bash|python", flagMode)
		}
	}
	return detectMode(resolveShell(c))
}
