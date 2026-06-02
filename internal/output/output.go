// Package output handles terminal output formatting (Human TTY-colored tables
// and machine-readable JSON), selected via the persistent --json flag.
package output

import "io"

// Formatter renders a value, dispatching on its runtime type.
type Formatter interface {
	Print(v any) error
}

// New returns a JSON formatter when jsonMode is true, otherwise a Human formatter.
func New(w io.Writer, jsonMode bool) Formatter {
	if jsonMode {
		return &JSON{W: w}
	}
	return &Human{W: w}
}
