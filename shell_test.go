package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/urfave/cli/v2"
)

// cliCtx builds a *cli.Context with an optional --mode value and positional
// args, for exercising resolveShell/resolveMode without the real app.
func cliCtx(t *testing.T, mode string, args ...string) *cli.Context {
	t.Helper()
	set := flag.NewFlagSet("test", flag.ContinueOnError)
	set.String("mode", mode, "")
	if err := set.Parse(args); err != nil {
		t.Fatal(err)
	}
	return cli.NewContext(nil, set, nil)
}

// envValue returns the value of key in a KEY=VALUE env slice, or "".
func envValue(env []string, key string) string {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return e[len(prefix):]
		}
	}
	return ""
}

func TestResolveShell(t *testing.T) {
	cases := []struct {
		name string
		arg  string
		env  string
		want string
	}{
		{"explicit arg wins", "/bin/zsh", "/bin/sh", "/bin/zsh"},
		{"env fallback", "", "/bin/zsh", "/bin/zsh"},
		{"both empty", "", "", "sh"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("SHELL", c.env)
			if got := resolveShell(cliCtx(t, "", c.arg)); got != c.want {
				t.Fatalf("resolveShell(%q) = %q, want %q", c.arg, got, c.want)
			}
		})
	}
}

func TestDetectMode(t *testing.T) {
	cases := []struct {
		name    string
		shell   string
		want    string
		wantErr bool
	}{
		{"zsh path", "/bin/zsh", modeZsh, false},
		{"zsh bare", "zsh", modeZsh, false},
		{"zsh login", "-zsh", modeZsh, false},
		{"sh path", "/usr/bin/sh", modeSh, false},
		{"sh bare", "sh", modeSh, false},
		{"bash path", "/bin/bash", modeBash, false},
		{"bash bare", "bash", modeBash, false},
		{"bash login", "-bash", modeBash, false},
		{"python3 path", "/usr/bin/python3", modePython, false},
		{"python3 bare", "python3", modePython, false},
		{"python3 versioned", "/usr/local/bin/python3.13", modePython, false},
		{"python bare", "python", modePython, false},
		{"python3-config unsupported", "/usr/bin/python3-config", "", true},
		{"dash unsupported", "/bin/dash", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := detectMode(c.shell)
			if c.wantErr != (err != nil) {
				t.Fatalf("detectMode(%q) err = %v, wantErr %v", c.shell, err, c.wantErr)
			}
			if !c.wantErr && got != c.want {
				t.Fatalf("detectMode(%q) = %q, want %q", c.shell, got, c.want)
			}
		})
	}
}

func TestResolveMode(t *testing.T) {
	cases := []struct {
		name     string
		flagMode string
		shell    string
		want     string
		wantErr  bool
	}{
		{"explicit overrides shell", modeSh, "/bin/bash", modeSh, false},
		{"empty infers", "", "/bin/zsh", modeZsh, false},
		{"empty infers bash", "", "/bin/bash", modeBash, false},
		{"explicit bash", modeBash, "/bin/sh", modeBash, false},
		{"empty infers python", "", "/usr/bin/python3", modePython, false},
		{"explicit python", modePython, "/bin/sh", modePython, false},
		{"bad mode", "fish", "/bin/zsh", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveMode(cliCtx(t, c.flagMode, c.shell))
			if c.wantErr != (err != nil) {
				t.Fatalf("resolveMode(%q,%q) err = %v, wantErr %v", c.flagMode, c.shell, err, c.wantErr)
			}
			if !c.wantErr && got != c.want {
				t.Fatalf("resolveMode(%q,%q) = %q, want %q", c.flagMode, c.shell, got, c.want)
			}
		})
	}
}

func TestWithEnv(t *testing.T) {
	base := []string{"ZDOTDIR=old", "PATH=/usr/bin", "HOME=/home/u"}
	got := withEnv(base, "ZDOTDIR=new")

	count := 0
	for _, e := range got {
		if strings.HasPrefix(e, "ZDOTDIR=") {
			count++
			if e != "ZDOTDIR=new" {
				t.Fatalf("ZDOTDIR = %q, want new", e)
			}
		}
	}
	if count != 1 {
		t.Fatalf("ZDOTDIR appears %d times, want 1", count)
	}
	if v := envValue(got, "PATH"); v != "/usr/bin" {
		t.Fatalf("PATH = %q, want untouched", v)
	}
	if v := envValue(got, "HOME"); v != "/home/u" {
		t.Fatalf("HOME = %q, want untouched", v)
	}
}

func TestGetCmdSh(t *testing.T) {
	t.Setenv("ENV", "/x/y")
	cmd, cleanup, err := getCmd("/bin/sh", modeSh)
	if err != nil {
		t.Fatalf("getCmd: %v", err)
	}
	if cmd.Args[0] != "/bin/sh" {
		t.Fatalf("Args[0] = %q, want /bin/sh", cmd.Args[0])
	}
	if v := envValue(cmd.Env, "RT_REAL_ENV"); v != "/x/y" {
		t.Fatalf("RT_REAL_ENV = %q, want /x/y", v)
	}
	envPath := envValue(cmd.Env, "ENV")
	if envPath == "" {
		t.Fatal("ENV not set")
	}
	b, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read rc: %v", err)
	}
	rc := string(b)
	for _, want := range []string{sepPayload, "$?", `. "$RT_REAL_ENV"`} {
		if !strings.Contains(rc, want) {
			t.Fatalf("rc missing %q:\n%s", want, rc)
		}
	}
	dir := filepath.Dir(envPath)
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("temp dir %s not removed: %v", dir, err)
	}
}

func TestGetCmdZsh(t *testing.T) {
	cmd, cleanup, err := getCmd("/bin/zsh", modeZsh)
	if err != nil {
		t.Fatalf("getCmd: %v", err)
	}
	if cmd.Args[0] != "/bin/zsh" {
		t.Fatalf("Args[0] = %q, want /bin/zsh", cmd.Args[0])
	}
	if len(cmd.Args) < 2 || cmd.Args[1] != "-i" {
		t.Fatalf("Args = %v, want second arg -i", cmd.Args)
	}
	if v := envValue(cmd.Env, "RT_REAL_ZDOTDIR"); v == "" {
		t.Fatal("RT_REAL_ZDOTDIR not set")
	}
	dir := envValue(cmd.Env, "ZDOTDIR")
	if dir == "" {
		t.Fatal("ZDOTDIR not set")
	}
	for _, name := range []string{".zshrc", ".zshenv"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, ".zshrc"))
	if err != nil {
		t.Fatalf("read .zshrc: %v", err)
	}
	rc := string(b)
	for _, want := range []string{"precmd_functions", sepPayload} {
		if !strings.Contains(rc, want) {
			t.Fatalf(".zshrc missing %q:\n%s", want, rc)
		}
	}
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("temp dir %s not removed: %v", dir, err)
	}
}

func TestGetCmdBash(t *testing.T) {
	cmd, cleanup, err := getCmd("/bin/bash", modeBash)
	if err != nil {
		t.Fatalf("getCmd: %v", err)
	}
	if cmd.Args[0] != "/bin/bash" {
		t.Fatalf("Args[0] = %q, want /bin/bash", cmd.Args[0])
	}
	if len(cmd.Args) < 4 || cmd.Args[1] != "--rcfile" || cmd.Args[3] != "-i" {
		t.Fatalf("Args = %v, want --rcfile <path> -i", cmd.Args)
	}
	if v := envValue(cmd.Env, "RT_REAL_BASHRC"); v == "" {
		t.Fatal("RT_REAL_BASHRC not set")
	}
	rcPath := cmd.Args[2]
	b, err := os.ReadFile(rcPath)
	if err != nil {
		t.Fatalf("read rc: %v", err)
	}
	rc := string(b)
	for _, want := range []string{sepPayload, "PROMPT_COMMAND", "$?", `"$RT_REAL_BASHRC"`} {
		if !strings.Contains(rc, want) {
			t.Fatalf("rc missing %q:\n%s", want, rc)
		}
	}
	dir := filepath.Dir(rcPath)
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("temp dir %s not removed: %v", dir, err)
	}
}

func TestGetCmdPython(t *testing.T) {
	t.Setenv("PYTHONSTARTUP", "/x/y")
	cmd, cleanup, err := getCmd("/usr/bin/python3", modePython)
	if err != nil {
		t.Fatalf("getCmd: %v", err)
	}
	if cmd.Args[0] != "/usr/bin/python3" {
		t.Fatalf("Args[0] = %q, want /usr/bin/python3", cmd.Args[0])
	}
	if len(cmd.Args) < 2 || cmd.Args[1] != "-i" {
		t.Fatalf("Args = %v, want second arg -i", cmd.Args)
	}
	if v := envValue(cmd.Env, "RT_REAL_PYTHONSTARTUP"); v != "/x/y" {
		t.Fatalf("RT_REAL_PYTHONSTARTUP = %q, want /x/y", v)
	}
	if v := envValue(cmd.Env, "PYTHON_BASIC_REPL"); v != "1" {
		t.Fatalf("PYTHON_BASIC_REPL = %q, want 1", v)
	}
	startupPath := envValue(cmd.Env, "PYTHONSTARTUP")
	if startupPath == "" {
		t.Fatal("PYTHONSTARTUP not set")
	}
	b, err := os.ReadFile(startupPath)
	if err != nil {
		t.Fatalf("read startup: %v", err)
	}
	startup := string(b)
	for _, want := range []string{sepPayload, "_RtPrompt", "sys.excepthook", "RT_REAL_PYTHONSTARTUP"} {
		if !strings.Contains(startup, want) {
			t.Fatalf("startup missing %q:\n%s", want, startup)
		}
	}
	dir := filepath.Dir(startupPath)
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("temp dir %s not removed: %v", dir, err)
	}
}

func TestGetCmdUnsupported(t *testing.T) {
	if _, _, err := getCmd("/bin/dash", "dash"); err == nil {
		t.Fatal("getCmd with unsupported mode: want error, got nil")
	}
}
