package main

import (
	"encoding/base64"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// clipboardCommands returns the native clipboard commands to try, in order, for
// a given GOOS. Each command reads the text from stdin.
func clipboardCommands(goos string) [][]string {
	switch goos {
	case "darwin":
		return [][]string{{"pbcopy"}}
	case "linux":
		return [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}}
	default:
		return nil
	}
}

// setClipboard copies text to the system clipboard: native tool first, OSC 52
// fallback. Failures are silent in the PoC.
func setClipboard(text string) {
	for _, argv := range clipboardCommands(runtime.GOOS) {
		if runClipboard(argv, text) {
			return
		}
	}
	osc52(text)
}

// runClipboard pipes text into a clipboard command and reports success.
func runClipboard(argv []string, text string) bool {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run() == nil
}

// osc52 emits the OSC 52 clipboard sequence. Some terminals disable or cap it,
// but it is the only fallback when no native tool is available.
func osc52(text string) {
	payload := base64.StdEncoding.EncodeToString([]byte(text))
	outMu.Lock()
	_, _ = os.Stdout.WriteString(ansiOsc("52;c;" + payload))
	outMu.Unlock()
}
