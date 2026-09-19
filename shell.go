package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type rcScript struct {
	path    string
	cleanup func()
}

// resolveShell picks the shell to run: an explicit positional argument wins,
// then $SHELL, then "sh".
func resolveShell(arg string) string {
	if arg != "" {
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
	switch base {
	case "zsh":
		return modeZsh, nil
	case "sh":
		return modeSh, nil
	default:
		return "", fmt.Errorf("unsupported shell %q: use --mode sh|zsh", shell)
	}
}

// chooseMode validates an explicit --mode, otherwise infers it from the shell.
func chooseMode(flagMode, shell string) (string, error) {
	if flagMode != "" {
		switch flagMode {
		case modeSh, modeZsh:
			return flagMode, nil
		default:
			return "", fmt.Errorf("unsupported mode %q: use --mode sh|zsh", flagMode)
		}
	}
	return detectMode(shell)
}

// withEnv drops inherited entries whose key appears in kv, then appends kv.
// exec.Cmd.Env does not dedupe and getenv returns the first match, so blindly
// appending would let an inherited variable shadow the one we set.
func withEnv(base []string, kv ...string) []string {
	keys := make(map[string]bool, len(kv))
	for _, e := range kv {
		k, _, _ := strings.Cut(e, "=")
		keys[k] = true
	}
	out := make([]string, 0, len(base)+len(kv))
	for _, e := range base {
		k, _, _ := strings.Cut(e, "=")
		if keys[k] {
			continue
		}
		out = append(out, e)
	}
	return append(out, kv...)
}

// getCmd builds the shell command with the rc injection appropriate to mode,
// returning a cleanup closure that removes any temp files.
func getCmd(shell, mode string) (*exec.Cmd, func(), error) {
	switch mode {
	case modeZsh:
		return zshCmd(shell)
	case modeSh:
		return shCmd(shell)
	default:
		return nil, nil, fmt.Errorf("unsupported mode %q", mode)
	}
}

// writeShRc creates a temp rc file that sources the user's original $ENV (if
// any), then installs the separator as the leading part of PS1 while preserving
// whatever prompt they set. sh is launched with ENV pointing here.
func writeShRc() (rcScript, error) {
	dir, err := os.MkdirTemp("", "rt-*")
	if err != nil {
		return rcScript{}, err
	}
	path := filepath.Join(dir, "rc")
	// $'...' ANSI-C quoting embeds literal ESC / BEL in PS1. $? stays literal
	// inside $'...' so sh expands it on every prompt; "${PS1-}" is expanded once
	// at source time, after the user's file has run.
	hook := "if [ -n \"$RT_REAL_ENV\" ] && [ -f \"$RT_REAL_ENV\" ]; then . \"$RT_REAL_ENV\"; fi\n" +
		"PS1=$'\\x1b]" + sepPayload + ";$?\\x07'\"${PS1-}\"\n"
	if werr := os.WriteFile(path, []byte(hook), 0o600); werr != nil {
		_ = os.RemoveAll(dir)
		return rcScript{}, werr
	}
	return rcScript{path: path, cleanup: func() { _ = os.RemoveAll(dir) }}, nil
}

// shCmd runs shell with ENV pointed at the generated rc, preserving the user's
// original ENV via RT_REAL_ENV.
func shCmd(shell string) (*exec.Cmd, func(), error) {
	script, err := writeShRc()
	if err != nil {
		return nil, nil, err
	}
	realENV := os.Getenv("ENV")
	cmd := exec.Command(shell)
	cmd.Env = withEnv(os.Environ(), "ENV="+script.path, "RT_REAL_ENV="+realENV, "REVERSE_TERMINAL=1")
	return cmd, script.cleanup, nil
}

// realZDOTDIR is the directory holding the user's real zsh dotfiles: $ZDOTDIR
// if set, else the home directory.
func realZDOTDIR() string {
	if z := os.Getenv("ZDOTDIR"); z != "" {
		return z
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// writeZshRc creates a temp dir to serve as ZDOTDIR. Its .zshenv and .zshrc
// source the user's real counterparts, and .zshrc installs a precmd hook that
// prints the separator (with the last exit code) before every prompt.
func writeZshRc() (string, func(), error) {
	dir, err := os.MkdirTemp("", "rt-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	zshenv := "# rt: source the user's real zshenv so PATH/options survive.\n" +
		"if [[ -n $RT_REAL_ZDOTDIR && -f $RT_REAL_ZDOTDIR/.zshenv ]]; then\n" +
		"  source $RT_REAL_ZDOTDIR/.zshenv\n" +
		"fi\n"
	if werr := os.WriteFile(filepath.Join(dir, ".zshenv"), []byte(zshenv), 0o600); werr != nil {
		cleanup()
		return "", nil, werr
	}

	// precmd output is not part of PS1, so the marker needs no %{...%} width
	// wrapping. __rt_ec must be captured first: only the first hook sees the
	// true $?. typeset -ga guards against setopt nounset.
	zshrc := fmt.Sprintf(`# rt: source the user's real zshrc, then install the prompt marker.
if [[ -n $RT_REAL_ZDOTDIR && -f $RT_REAL_ZDOTDIR/.zshrc ]]; then
  source $RT_REAL_ZDOTDIR/.zshrc
fi

__rt_marker() {
  local __rt_ec=$?
  print -rn -- $'\x1b]%s;'"$__rt_ec"$'\x07'
}
typeset -ga precmd_functions
precmd_functions=(__rt_marker $precmd_functions)
`, sepPayload)
	if werr := os.WriteFile(filepath.Join(dir, ".zshrc"), []byte(zshrc), 0o600); werr != nil {
		cleanup()
		return "", nil, werr
	}
	return dir, cleanup, nil
}

// zshCmd runs shell interactively with ZDOTDIR pointed at the generated shim
// dir, preserving the user's real ZDOTDIR via RT_REAL_ZDOTDIR.
func zshCmd(shell string) (*exec.Cmd, func(), error) {
	dir, cleanup, err := writeZshRc()
	if err != nil {
		return nil, nil, err
	}
	cmd := exec.Command(shell, "-i")
	cmd.Env = withEnv(os.Environ(),
		"ZDOTDIR="+dir,
		"RT_REAL_ZDOTDIR="+realZDOTDIR(),
		"REVERSE_TERMINAL=1",
	)
	return cmd, cleanup, nil
}
