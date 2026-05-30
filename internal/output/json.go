package output

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/emailable/emailable-cli/internal/ui"
)

// RawJSONProvider is implemented by API response types that retain the server
// response. When a value carries raw bytes, machine output is built from those
// instead of re-encoding the typed struct, so nullable fields and any field the
// struct doesn't model are preserved — the contract the README advertises.
//
// This is a structural passthrough, not byte-for-byte: every key, value, null,
// and field order is preserved, but insignificant whitespace is normalized to
// the formatter's shape (see documentBytes) so output stays consistent and
// colorizable regardless of how the API formatted the body.
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

// JSON renders a value as JSON — pretty by default, one line when Compact,
// colorized on a TTY. A set Query (--jq) filters the value before printing.
type JSON struct {
	W       io.Writer
	Query   *Query
	Compact bool
}

// FilterError distinguishes a --jq runtime error from an I/O error, so the
// streaming path can skip an event whose filter errored instead of aborting.
type FilterError struct{ Err error }

func (e *FilterError) Error() string { return e.Err.Error() }
func (e *FilterError) Unwrap() error { return e.Err }

// marshalDocument renders v's JSON document — the raw API body when present
// (only whitespace reshaped, so unmodeled fields and nulls survive), else the
// typed encoding. Shared by the formatter and file writes so they can't drift.
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
		// Malformed raw: fall through to typed encoding below.
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

// printFiltered writes each --jq result on its own line. Strings print raw
// (unquoted, like `jq -r`); everything else as JSON.
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
	// HTML escaping off so URLs and angle brackets survive, the way jq emits them.
	enc.SetEscapeHTML(false)
	if !j.Compact {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// ANSI escape sequences for the JSON palette. Raw codes (not lipgloss): the
// global lipgloss renderer probes process stdio at init and can drop a
// TTY-detected writer to uncolored output, whereas raw codes are deterministic
// and testable.
const (
	jsonAnsiReset  = "\x1b[0m"
	jsonAnsiKey    = "\x1b[1;36m" // bold cyan
	jsonAnsiString = "\x1b[32m"   // green
	jsonAnsiNumber = "\x1b[33m"   // yellow
	jsonAnsiBool   = "\x1b[1;33m" // bold yellow
	jsonAnsiNull   = "\x1b[2m"    // dim
)

// colorizeJSON wraps each JSON token in src with ANSI styling. src must be
// well-formed JSON (the bytes coming out of encoding/json), so this is a
// pragmatic scanner rather than a full parser: strings are extracted with
// escape-aware bounds, numbers are walked greedily over [0-9.eE+-], and
// `true`/`false`/`null` are matched as literals. Anything else (whitespace,
// structural punctuation) is copied through unchanged.
//
// Key vs string disambiguation: after closing a string we peek past any
// spaces/tabs; a following ':' means we just rendered an object key.
func colorizeJSON(src []byte) []byte {
	out := bytes.NewBuffer(make([]byte, 0, len(src)*2))
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '"':
			end := scanString(src, i)
			tok := src[i:end]
			// A following ':' (past spaces/tabs only — no newlines appear
			// between key and colon) means this string is an object key.
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

// wrap writes prefix + tok + reset to out.
func wrap(out *bytes.Buffer, prefix string, tok []byte) {
	out.WriteString(prefix)
	out.Write(tok)
	out.WriteString(jsonAnsiReset)
}

// scanString returns the index one past the closing quote of the JSON
// string starting at src[start] (which must be '"'). Backslash escapes are
// skipped so an escaped quote (\") doesn't terminate the string early.
func scanString(src []byte, start int) int {
	i := start + 1
	for i < len(src) {
		switch src[i] {
		case '\\':
			// Skip the escape and the byte it escapes. For \uXXXX the four
			// hex digits get walked as ordinary unescaped bytes in the next
			// iteration — that's fine; none of them are '"' or '\\'.
			i += 2
		case '"':
			return i + 1
		default:
			i++
		}
	}
	return i
}

// scanNumber returns the index one past the last byte of the JSON number
// starting at src[start]. The accepted character set is intentionally a
// superset (it would match malformed numbers too), but encoding/json only
// emits valid numbers, so over-acceptance is harmless here.
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

// hasLiteral reports whether src starting at i exactly matches lit.
func hasLiteral(src []byte, i int, lit string) bool {
	if i+len(lit) > len(src) {
		return false
	}
	return string(src[i:i+len(lit)]) == lit
}
