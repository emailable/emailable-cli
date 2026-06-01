package ui

// ANSI helpers take an explicit tty bool; when false they return s unchanged.

const (
	ansiReset = "\033[0m"
	ansiBold  = "\033[1m"
	ansiDim   = "\033[2m"
	ansiCyan  = "\033[36m"
)

// Cyan styles s cyan when tty is true.
func Cyan(s string, tty bool) string {
	if !tty {
		return s
	}
	return ansiCyan + s + ansiReset
}

// Dim styles s dim when tty is true.
func Dim(s string, tty bool) string {
	if !tty {
		return s
	}
	return ansiDim + s + ansiReset
}

// Heading styles s as a bold cyan heading when tty is true.
func Heading(s string, tty bool) string {
	if !tty {
		return s
	}
	return ansiBold + ansiCyan + s + ansiReset
}
