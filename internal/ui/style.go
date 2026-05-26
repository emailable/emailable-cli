package ui

// ANSI styling helpers. Each helper takes an explicit tty bool so the caller
// can detect TTY-ness once (typically against os.Stdout) and propagate the
// decision through a render pass. When tty is false the helpers return s
// unchanged, so piped output stays clean.

const (
	ansiReset = "\033[0m"
	ansiBold  = "\033[1m"
	ansiDim   = "\033[2m"
	ansiCyan  = "\033[36m"
)

// Cyan wraps s in ANSI cyan codes when tty is true, otherwise returns s as-is.
func Cyan(s string, tty bool) string {
	if !tty {
		return s
	}
	return ansiCyan + s + ansiReset
}

// Dim wraps s in ANSI dim codes when tty is true, otherwise returns s as-is.
func Dim(s string, tty bool) string {
	if !tty {
		return s
	}
	return ansiDim + s + ansiReset
}

// Heading renders a section heading: bold + cyan when tty, plain otherwise.
// Used for the uppercase section labels in help output (USAGE, FLAGS, etc.).
func Heading(s string, tty bool) string {
	if !tty {
		return s
	}
	return ansiBold + ansiCyan + s + ansiReset
}
