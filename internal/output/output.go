// Package output handles terminal output formatting. Two formats are
// supported: JSON (machine-readable) and Human (TTY-colored, table-style).
//
// Callers pick the format from the persistent --json flag and call Print on
// the resulting Formatter.
package output

import "io"

// Formatter renders a value, dispatching on its runtime type.
type Formatter interface {
	Print(v any) error
}

// New returns a JSON formatter when jsonMode is true, otherwise a Human one.
func New(w io.Writer, jsonMode bool) Formatter {
	if jsonMode {
		return &JSON{W: w}
	}
	return &Human{W: w}
}
