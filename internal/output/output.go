// Package output handles terminal output formatting. Two formats are
// supported: JSON (machine-readable) and Human (TTY-colored, table-style).
//
// Callers pick the format from the persistent --json flag and call Print on
// the resulting Formatter.
package output

import "io"

// Formatter is implemented by both JSON and Human formatters. Print should
// dispatch on the runtime type of v (single verify result, batch status,
// account, []VerifyResult, etc) and render appropriately. See human.go for
// the shapes Human supports.
type Formatter interface {
	Print(v any) error
}

// New returns the appropriate Formatter for the requested format.
// jsonMode=true returns a JSON formatter; otherwise Human.
func New(w io.Writer, jsonMode bool) Formatter {
	if jsonMode {
		return &JSON{W: w}
	}
	return &Human{W: w}
}
