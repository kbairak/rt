package main

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

// prepareTerm puts stdin in raw mode and returns a restore closure that shows
// the cursor, resets attributes and restores the original terminal state.
func prepareTerm() (func(), error) {
	state, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return nil, fmt.Errorf("raw: %w", err)
	}
	return func() {
		os.Stdout.WriteString(ansiShowCursor + ansiReset)
		_ = term.Restore(int(os.Stdin.Fd()), state)
	}, nil
}
