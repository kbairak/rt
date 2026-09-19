package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

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
	case "zsh":
		return zshCmd(shell)
	case "bash":
		return bashCmd(shell)
	case "sh":
		return shCmd(shell)
	case "python":
		return pythonCmd(shell)
	default:
		return nil, nil, fmt.Errorf("unsupported mode %q", mode)
	}
}

// shCmd runs shell with ENV pointed at a generated rc that sources the user's
// original $ENV (if any), then installs the separator as the leading part of
// PS1 while preserving whatever prompt they set. $'...' ANSI-C quoting embeds
// literal ESC / BEL in PS1. $? stays literal inside $'...' so sh expands it on
// every prompt; "${PS1-}" is expanded once at source time, after the user's
// file has run.
func shCmd(shell string) (*exec.Cmd, func(), error) {
	dir, err := os.MkdirTemp("", "rt-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, "rc")
	hook := strings.Join([]string{
		`if [ -n "$RT_REAL_ENV" ] && [ -f "$RT_REAL_ENV" ]; then . "$RT_REAL_ENV"; fi`,
		`PS1=$'\x1b]` + ansiRtPayload + `;$?\x07'"${PS1-}"`,
		"",
	}, "\n")
	if werr := os.WriteFile(path, []byte(hook), 0o600); werr != nil {
		cleanup()
		return nil, nil, werr
	}
	realENV := os.Getenv("ENV")
	cmd := exec.Command(shell)
	cmd.Env = withEnv(os.Environ(), "ENV="+path, "RT_REAL_ENV="+realENV, "REVERSE_TERMINAL=1")
	return cmd, cleanup, nil
}

// bashCmd runs bash interactively with --rcfile pointed at a generated rc that
// sources the user's real ~/.bashrc, then installs a PROMPT_COMMAND hook that
// prints the separator (with the last command's status) before every prompt.
// PROMPT_COMMAND output is not part of PS1, so the marker needs no \[...\] width
// wrapping. bash --rcfile replaces ~/.bashrc for interactive non-login shells.
func bashCmd(shell string) (*exec.Cmd, func(), error) {
	dir, err := os.MkdirTemp("", "rt-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, "rc")
	// Prepend so __rt_marker is the first hook and sees the true $?. Bash 5.1+
	// allows PROMPT_COMMAND to be an array; handle both forms.
	hook := strings.Join([]string{
		`if [ -n "$RT_REAL_BASHRC" ] && [ -f "$RT_REAL_BASHRC" ]; then . "$RT_REAL_BASHRC"; fi`,
		`__rt_marker() { printf '\033]` + ansiRtPayload + `;%d\a' "$?"; }`,
		`if [[ $(declare -p PROMPT_COMMAND 2>/dev/null) == "declare -a"* ]]; then`,
		`  PROMPT_COMMAND=(__rt_marker "${PROMPT_COMMAND[@]}")`,
		`else`,
		`  PROMPT_COMMAND="__rt_marker${PROMPT_COMMAND:+;$PROMPT_COMMAND}"`,
		`fi`,
		"",
	}, "\n")
	if werr := os.WriteFile(path, []byte(hook), 0o600); werr != nil {
		cleanup()
		return nil, nil, werr
	}
	// Bash has no environment override for ~/.bashrc, so the default for an
	// interactive non-login shell is used.
	realBashrc := ""
	if home, herr := os.UserHomeDir(); herr == nil {
		realBashrc = filepath.Join(home, ".bashrc")
	}
	cmd := exec.Command(shell, "--rcfile", path, "-i")
	cmd.Env = withEnv(os.Environ(),
		"RT_REAL_BASHRC="+realBashrc,
		"REVERSE_TERMINAL=1",
	)
	return cmd, cleanup, nil
}

// zshCmd runs shell interactively with ZDOTDIR pointed at a generated shim dir
// whose .zshenv and .zshrc source the user's real counterparts; .zshrc installs
// a precmd hook that prints the separator (with the last exit code) before every
// prompt. The real ZDOTDIR is preserved via RT_REAL_ZDOTDIR.
func zshCmd(shell string) (*exec.Cmd, func(), error) {
	dir, err := os.MkdirTemp("", "rt-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	zshenv := strings.Join([]string{
		`# rt: source the user's real zshenv so PATH/options survive.`,
		`if [[ -n $RT_REAL_ZDOTDIR && -f $RT_REAL_ZDOTDIR/.zshenv ]]; then`,
		`  source $RT_REAL_ZDOTDIR/.zshenv`,
		`fi`,
		"",
	}, "\n")
	if werr := os.WriteFile(filepath.Join(dir, ".zshenv"), []byte(zshenv), 0o600); werr != nil {
		cleanup()
		return nil, nil, werr
	}

	// precmd output is not part of PS1, so the marker needs no %{...%} width
	// wrapping. __rt_ec must be captured first: only the first hook sees the
	// true $?. typeset -ga guards against setopt nounset.
	zshrc := fmt.Sprintf(strings.Join([]string{
		`# rt: source the user's real zshrc, then install the prompt marker.`,
		`if [[ -n $RT_REAL_ZDOTDIR && -f $RT_REAL_ZDOTDIR/.zshrc ]]; then`,
		`  source $RT_REAL_ZDOTDIR/.zshrc`,
		`fi`,
		"",
		`__rt_marker() {`,
		`  local __rt_ec=$?`,
		`  print -rn -- $'\x1b]%s;'"$__rt_ec"$'\x07'`,
		`}`,
		`typeset -ga precmd_functions`,
		`precmd_functions=(__rt_marker $precmd_functions)`,
		"",
	}, "\n"), ansiRtPayload)
	if werr := os.WriteFile(filepath.Join(dir, ".zshrc"), []byte(zshrc), 0o600); werr != nil {
		cleanup()
		return nil, nil, werr
	}

	// $ZDOTDIR if set, else the home directory.
	realZDOTDIR := os.Getenv("ZDOTDIR")
	if realZDOTDIR == "" {
		if home, herr := os.UserHomeDir(); herr == nil {
			realZDOTDIR = home
		}
	}

	cmd := exec.Command(shell, "-i")
	cmd.Env = withEnv(os.Environ(),
		"ZDOTDIR="+dir,
		"RT_REAL_ZDOTDIR="+realZDOTDIR,
		"REVERSE_TERMINAL=1",
	)
	return cmd, cleanup, nil
}

// pythonCmd runs the interpreter interactively with PYTHONSTARTUP pointed at a
// generated shim that runs the user's original startup (if any), then installs a
// prompt object whose __str__ emits the separator (with the last statement's
// status) before every primary prompt. Classic REPL calls str(sys.ps1) exactly
// once per prompt, so writing the marker as a side effect keeps it out of the
// prompt string and immune to readline redraws. PYTHON_BASIC_REPL forces the
// classic REPL: the 3.13+ _pyrepl calls __str__ on every keystroke, which would
// emit spurious markers. The real startup is preserved via RT_REAL_PYTHONSTARTUP.
func pythonCmd(shell string) (*exec.Cmd, func(), error) {
	dir, err := os.MkdirTemp("", "rt-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, "startup.py")
	startup := fmt.Sprintf(strings.Join([]string{
		`# rt: run the user's real PYTHONSTARTUP, then install the prompt marker.`,
		`import os, sys`,
		"",
		`_rt_real = os.environ.get("RT_REAL_PYTHONSTARTUP", "")`,
		`if _rt_real and os.path.isfile(_rt_real):`,
		`    exec(compile(open(_rt_real).read(), _rt_real, "exec"), globals())`,
		"",
		`_rt_status = [0]`,
		`_rt_orig_excepthook = sys.excepthook`,
		"",
		`def _rt_excepthook(exc_type, exc_value, tb):`,
		`    _rt_status[0] = 1`,
		`    _rt_orig_excepthook(exc_type, exc_value, tb)`,
		"",
		`sys.excepthook = _rt_excepthook`,
		"",
		`_rt_user_ps1 = str(getattr(sys, "ps1", ">>> "))`,
		"",
		`class _RtPrompt:`,
		`    def __str__(self):`,
		`        sys.stdout.write("\x1b]%s;%%d\x07" %% _rt_status[0])`,
		`        sys.stdout.flush()`,
		`        _rt_status[0] = 0`,
		`        return _rt_user_ps1`,
		"",
		`sys.ps1 = _RtPrompt()`,
		"",
	}, "\n"), ansiRtPayload)
	if werr := os.WriteFile(path, []byte(startup), 0o600); werr != nil {
		cleanup()
		return nil, nil, werr
	}
	realStartup := os.Getenv("PYTHONSTARTUP")
	cmd := exec.Command(shell, "-i")
	cmd.Env = withEnv(os.Environ(),
		"PYTHONSTARTUP="+path,
		"RT_REAL_PYTHONSTARTUP="+realStartup,
		"PYTHON_BASIC_REPL=1",
		"REVERSE_TERMINAL=1",
	)
	return cmd, cleanup, nil
}
