module reverse-terminal

go 1.26.0

require (
	github.com/creack/pty v1.1.24
	github.com/hinshun/vt10x v0.0.0-20220301184237-5011da428d02
	golang.org/x/term v0.46.0
)

require golang.org/x/sys v0.48.0 // indirect

replace github.com/hinshun/vt10x => github.com/hinshun/vt10x v0.0.0-20220301184237-5011da428d02
