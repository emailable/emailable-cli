package output

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/emailable/emailable-cli/internal/ui"
)

// RawJSONProvider is implemented by API response types that retain the raw
// server body. Using it instead of re-encoding the typed struct preserves
// nullable fields and unmodeled keys — the contract the README advertises.
// Whitespace is normalized to the formatter's shape (compact vs. pretty).
type RawJSONProvider interface {
	RawJSON() []byte
}

func rawBytes(v any) ([]byte, bool) {
	p, ok := v.(RawJSONProvider)
	if !ok {
		return nil, false
	}
	raw := p.RawJSON()
	if len(raw) == 0 {
		return nil, false
	}
	return raw, true
}

// JSON renders a value as JSON, optionally filtering with a jq query.
type JSON struct {
	W       io.Writer
	Query   *Query
	Compact bool
}

// FilterError distinguishes a --jq runtime error from an I/O error so the
// streaming path can skip a failed event instead of aborting.
type FilterError struct{ Err error }

func (e *FilterError) Error() string { return e.Err.Error() }
func (e *FilterError) Unwrap() error { return e.Err }

func marshalDocument(v any, compact bool) ([]byte, error) {
	if raw, ok := rawBytes(v); ok {
		var buf bytes.Buffer
		var err error
		if compact {
			err = json.Compact(&buf, raw)
		} else {
			err = json.Indent(&buf, raw, "", "  ")
		}
		if err == nil {
			buf.WriteByte('\n')
			return buf.Bytes(), nil
		}
		// Malformed raw bytes — fall through to typed encoding.
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	if !compact {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (j *JSON) documentBytes(v any) ([]byte, error) {
	return marshalDocument(v, j.Compact)
}

// Print writes v as JSON to j.W, applying j.Query if set.
func (j *JSON) Print(v any) error {
	if j.Query != nil {
		return j.printFiltered(v)
	}
	out, err := j.documentBytes(v)
	if err != nil {
		return err
	}
	if ui.IsTTY(j.W) {
		out = colorizeJSON(out)
	}
	_, err = j.W.Write(out)
	return err
}

func (j *JSON) printFiltered(v any) error {
	doc, err := j.documentBytes(v)
	if err != nil {
		return err
	}
	var input any
	if err := json.Unmarshal(doc, &input); err != nil {
		return err
	}
	results, err := j.Query.run(input)
	if err != nil {
		return &FilterError{Err: err}
	}

	tty := ui.IsTTY(j.W)
	var out bytes.Buffer
	for _, r := range results {
		if s, ok := r.(string); ok {
			out.WriteString(s)
			out.WriteByte('\n')
			continue
		}
		b, err := j.encodeResult(r)
		if err != nil {
			return err
		}
		if tty {
			b = colorizeJSON(b)
		}
		out.Write(b)
		out.WriteByte('\n')
	}
	_, err = j.W.Write(out.Bytes())
	return err
}

func (j *JSON) encodeResult(r any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // match jq: URLs and angle brackets pass through unescaped
	if !j.Compact {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Raw ANSI codes rather than lipgloss: lipgloss probes process stdio at init
// and can drop color for a TTY-detected writer; raw codes are deterministic.
const (
	jsonAnsiReset  = "\x1b[0m"
	jsonAnsiKey    = "\x1b[1;36m" // bold cyan
	jsonAnsiString = "\x1b[32m"   // green
	jsonAnsiNumber = "\x1b[33m"   // yellow
	jsonAnsiBool   = "\x1b[1;33m" // bold yellow
	jsonAnsiNull   = "\x1b[2m"    // dim
)

// colorizeJSON applies ANSI color to well-formed JSON from encoding/json.
// Key vs. value disambiguation: after closing a string, peek past spaces/tabs;
// a ':' means the string was an object key.
func colorizeJSON(src []byte) []byte {
	out := bytes.NewBuffer(make([]byte, 0, len(src)*2))
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '"':
			end := scanString(src, i)
			tok := src[i:end]
			k := end
			for k < len(src) && (src[k] == ' ' || src[k] == '\t') {
				k++
			}
			if k < len(src) && src[k] == ':' {
				wrap(out, jsonAnsiKey, tok)
			} else {
				wrap(out, jsonAnsiString, tok)
			}
			i = end
		case c == '-' || (c >= '0' && c <= '9'):
			end := scanNumber(src, i)
			wrap(out, jsonAnsiNumber, src[i:end])
			i = end
		case c == 't' && hasLiteral(src, i, "true"):
			wrap(out, jsonAnsiBool, []byte("true"))
			i += 4
		case c == 'f' && hasLiteral(src, i, "false"):
			wrap(out, jsonAnsiBool, []byte("false"))
			i += 5
		case c == 'n' && hasLiteral(src, i, "null"):
			wrap(out, jsonAnsiNull, []byte("null"))
			i += 4
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.Bytes()
}

func wrap(out *bytes.Buffer, prefix string, tok []byte) {
	out.WriteString(prefix)
	out.Write(tok)
	out.WriteString(jsonAnsiReset)
}

// scanString returns the index one past the closing quote. Backslash escapes
// skip two bytes so \" doesn't terminate early.
func scanString(src []byte, start int) int {
	i := start + 1
	for i < len(src) {
		switch src[i] {
		case '\\':
			i += 2
		case '"':
			return i + 1
		default:
			i++
		}
	}
	return i
}

func scanNumber(src []byte, start int) int {
	i := start + 1
	for i < len(src) {
		c := src[i]
		if (c >= '0' && c <= '9') || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-' {
			i++
			continue
		}
		break
	}
	return i
}

func hasLiteral(src []byte, i int, lit string) bool {
	if i+len(lit) > len(src) {
		return false
	}
	return string(src[i:i+len(lit)]) == lit
}
