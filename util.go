package main

import "bytes"

// indexFrom returns the index of sep in data at or after start, or -1. Like
// bytes.Index with a Python-style start offset, but the returned index is
// absolute. start is clamped to [0, len(data)].
func indexFrom(data, sep []byte, start int) int {
	if start < 0 {
		start = 0
	}
	if start > len(data) {
		return -1
	}
	i := bytes.Index(data[start:], sep)
	if i < 0 {
		return -1
	}
	return i + start
}

// onlyHasCtrlLs reports whether data is a non-empty run of CTRL-L bytes
// (^\x0c+$). Empty and mixed reads are false.
func onlyHasCtrlLs(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	for _, b := range data {
		if b != 0x0c {
			return false
		}
	}
	return true
}

// onlyHasCopyKeys reports whether data is a non-empty run of the copy trigger
// byte (CTRL-^, 0x1e). Empty and mixed reads are false.
func onlyHasCopyKeys(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	for _, b := range data {
		if b != copyKey {
			return false
		}
	}
	return true
}

// parseInt parses a non-negative decimal integer; ok is false on an empty
// string or any non-digit byte.
func parseInt(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// itoa formats a non-negative integer.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
