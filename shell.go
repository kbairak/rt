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

type ShellEnv struct {
	Env     []string
	Args    []string
	Cleanup func()
}

func getShellEnv(shell string) *ShellEnv {
	dir, err := os.MkdirTemp("", "rt-*")
	if err != nil {
		panic(err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	hookPath := filepath.Join(dir, hookFileName)
	if err := os.WriteFile(hookPath, []byte(rtHookScript), 0o600); err != nil {
		cleanup()
		panic(err)
	}

	base := filepath.Base(shell)
	switch base {
	case "zsh":
		zshrc := `source "` + hookPath + `"`
		if err := os.WriteFile(filepath.Join(dir, ".zshrc"), []byte(zshrc), 0o600); err != nil {
			cleanup()
			panic(err)
		}
		return &ShellEnv{
			Env:     append(os.Environ(), "ZDOTDIR="+dir),
			Cleanup: cleanup,
		}
	default:
		// bash/sh — use ENV (POSIX) or --rcfile (bash)
		return &ShellEnv{
			Env:     append(os.Environ(), "ENV="+hookPath),
			Args:    []string{"--rcfile", hookPath},
			Cleanup: cleanup,
		}
	}
}