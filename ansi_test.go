package main

import "testing"

func TestAnsiOsc(t *testing.T) {
	if got, want := ansiOsc("X;1"), "\x1b]X;1\x07"; got != want {
		t.Fatalf("ansiOsc = %q, want %q", got, want)
	}
}
