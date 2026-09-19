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

// chooseMode validates an explicit --mode, otherwise infers it from the shell.
func chooseMode(flagMode, shell string) (string, error) {
	if flagMode != "" {
		switch flagMode {
		case modeSh, modeZsh, modeBash, modePython:
			return flagMode, nil
		default:
			return "", fmt.Errorf("unsupported mode %q: use --mode sh|zsh|bash|python", flagMode)
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
	case modeBash:
		return bashCmd(shell)
	case modeSh:
		return shCmd(shell)
	case modePython:
		return pythonCmd(shell)
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

// realBashrc is the user's real bash rc file. Bash has no environment override
// for it, so the default for an interactive non-login shell is used.
func realBashrc() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".bashrc")
}

// writeBashRc creates a temp rc file that sources the user's real ~/.bashrc,
// then installs a PROMPT_COMMAND hook that prints the separator (with the last
// command's status) before every prompt. PROMPT_COMMAND output is not part of
// PS1, so the marker needs no \[...\] width wrapping. bash --rcfile replaces
// ~/.bashrc for interactive non-login shells.
func writeBashRc() (rcScript, error) {
	dir, err := os.MkdirTemp("", "rt-*")
	if err != nil {
		return rcScript{}, err
	}
	path := filepath.Join(dir, "rc")
	// Prepend so __rt_marker is the first hook and sees the true $?. Bash 5.1+
	// allows PROMPT_COMMAND to be an array; handle both forms.
	hook := "if [ -n \"$RT_REAL_BASHRC\" ] && [ -f \"$RT_REAL_BASHRC\" ]; then . \"$RT_REAL_BASHRC\"; fi\n" +
		"__rt_marker() { printf '\\033]" + sepPayload + ";%d\\a' \"$?\"; }\n" +
		"if [[ $(declare -p PROMPT_COMMAND 2>/dev/null) == \"declare -a\"* ]]; then\n" +
		"  PROMPT_COMMAND=(__rt_marker \"${PROMPT_COMMAND[@]}\")\n" +
		"else\n" +
		"  PROMPT_COMMAND=\"__rt_marker${PROMPT_COMMAND:+;$PROMPT_COMMAND}\"\n" +
		"fi\n"
	if werr := os.WriteFile(path, []byte(hook), 0o600); werr != nil {
		_ = os.RemoveAll(dir)
		return rcScript{}, werr
	}
	return rcScript{path: path, cleanup: func() { _ = os.RemoveAll(dir) }}, nil
}

// bashCmd runs bash interactively with --rcfile pointed at the generated rc,
// preserving the user's real ~/.bashrc via RT_REAL_BASHRC.
func bashCmd(shell string) (*exec.Cmd, func(), error) {
	script, err := writeBashRc()
	if err != nil {
		return nil, nil, err
	}
	cmd := exec.Command(shell, "--rcfile", script.path, "-i")
	cmd.Env = withEnv(os.Environ(),
		"RT_REAL_BASHRC="+realBashrc(),
		"REVERSE_TERMINAL=1",
	)
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

// writePythonStartup creates a temp PYTHONSTARTUP file that runs the user's
// original startup (if any), then installs a prompt object whose __str__ emits
// the separator (with the last statement's status) before every primary prompt.
// Classic REPL calls str(sys.ps1) exactly once per prompt, so writing the marker
// as a side effect keeps it out of the prompt string and immune to readline
// redraws. PYTHON_BASIC_REPL forces the classic REPL: the 3.13+ _pyrepl calls
// __str__ on every keystroke, which would emit spurious markers.
func writePythonStartup() (rcScript, error) {
	dir, err := os.MkdirTemp("", "rt-*")
	if err != nil {
		return rcScript{}, err
	}
	path := filepath.Join(dir, "startup.py")
	startup := fmt.Sprintf(`# rt: run the user's real PYTHONSTARTUP, then install the prompt marker.
import os, sys

_rt_real = os.environ.get("RT_REAL_PYTHONSTARTUP", "")
if _rt_real and os.path.isfile(_rt_real):
    exec(compile(open(_rt_real).read(), _rt_real, "exec"), globals())

_rt_status = [0]
_rt_orig_excepthook = sys.excepthook

def _rt_excepthook(exc_type, exc_value, tb):
    _rt_status[0] = 1
    _rt_orig_excepthook(exc_type, exc_value, tb)

sys.excepthook = _rt_excepthook

_rt_user_ps1 = str(getattr(sys, "ps1", ">>> "))

class _RtPrompt:
    def __str__(self):
        sys.stdout.write("\x1b]%s;%%d\x07" %% _rt_status[0])
        sys.stdout.flush()
        _rt_status[0] = 0
        return _rt_user_ps1

sys.ps1 = _RtPrompt()
`, sepPayload)
	if werr := os.WriteFile(path, []byte(startup), 0o600); werr != nil {
		_ = os.RemoveAll(dir)
		return rcScript{}, werr
	}
	return rcScript{path: path, cleanup: func() { _ = os.RemoveAll(dir) }}, nil
}

// pythonCmd runs the interpreter interactively with PYTHONSTARTUP pointed at the
// generated shim, preserving the user's real startup via RT_REAL_PYTHONSTARTUP.
func pythonCmd(shell string) (*exec.Cmd, func(), error) {
	script, err := writePythonStartup()
	if err != nil {
		return nil, nil, err
	}
	realStartup := os.Getenv("PYTHONSTARTUP")
	cmd := exec.Command(shell, "-i")
	cmd.Env = withEnv(os.Environ(),
		"PYTHONSTARTUP="+script.path,
		"RT_REAL_PYTHONSTARTUP="+realStartup,
		"PYTHON_BASIC_REPL=1",
		"REVERSE_TERMINAL=1",
	)
	return cmd, script.cleanup, nil
}
