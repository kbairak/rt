package main

import (
	"os"
	"path/filepath"
)

const rtHookScript = `if [ -n "$ZSH_VERSION" ]; then
    PROMPT='RTMRK%# '
    RPROMPT=''
    PS2=''
    PS3=''
    unsetopt PROMPT_CR PROMPT_SP
elif [ -n "$BASH_VERSION" ]; then
    PS1='RTMRK$ '
fi
`

const hookFileName = "hook.sh"

// getShellEnv prepares a temp dir with a hook that injects the RTMRK
// prompt marker, returning the environment and argv to spawn the shell
// with, plus a cleanup that removes the temp dir.
func getShellEnv(shell string) (env []string, args []string, cleanup func(), err error) {
	dir, derr := os.MkdirTemp("", "rt-*")
	if derr != nil {
		return nil, nil, nil, derr
	}
	cleanup = func() { _ = os.RemoveAll(dir) }

	hookPath := filepath.Join(dir, hookFileName)
	if werr := os.WriteFile(hookPath, []byte(rtHookScript), 0o600); werr != nil {
		cleanup()
		return nil, nil, nil, werr
	}

	base := filepath.Base(shell)
	switch base {
	case "zsh":
		zshrc := `source "` + hookPath + `"`
		if werr := os.WriteFile(filepath.Join(dir, ".zshrc"), []byte(zshrc), 0o600); werr != nil {
			cleanup()
			return nil, nil, nil, werr
		}
		return append(os.Environ(), "ZDOTDIR="+dir), nil, cleanup, nil
	default:
		// bash/sh — use ENV (POSIX) or --rcfile (bash)
		return append(os.Environ(), "ENV="+hookPath), []string{"--rcfile", hookPath}, cleanup, nil
	}
}
