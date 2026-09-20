package main

import "testing"

// TestRecorderNilSafe verifies that a nil recorder (logging disabled) accepts
// every call without panicking.
func TestRecorderNilSafe(t *testing.T) {
	var r *recorder
	r.event("start")
	r.tx([]byte("a"))
	r.rx([]byte("b"))
	r.block(0, "line")
	r.Close()
}
