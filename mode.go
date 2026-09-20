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
		return "zsh", nil
	case base == "bash":
		return "bash", nil
	case base == "sh":
		return "sh", nil
	case base == "python" || base == "python3" || strings.HasPrefix(base, "python3."):
		return "python", nil
	case base == "ipython" || base == "ipython3" || strings.HasPrefix(base, "ipython3."):
		return "ipython", nil
	default:
		return "", fmt.Errorf("unsupported shell %q: use --mode sh|zsh|bash|python|ipython", shell)
	}
}

// resolveMode validates an explicit --mode, otherwise infers it from the shell.
func resolveMode(c *cli.Context) (string, error) {
	if flagMode := c.String("mode"); flagMode != "" {
		switch flagMode {
		case "sh", "zsh", "bash", "python", "ipython":
			return flagMode, nil
		default:
			return "", fmt.Errorf("unsupported mode %q: use --mode sh|zsh|bash|python|ipython", flagMode)
		}
	}
	return detectMode(resolveShell(c))
}
