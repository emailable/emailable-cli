package output

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/emailable/emailable-cli/internal/ui"
)

// RawJSONProvider is implemented by API response types that retain the
// verbatim server response. When a value carries raw bytes, machine output
// emits those (re-indented) instead of re-encoding the typed struct, so
// nullable fields and any field the struct doesn't model pass through
// unchanged — the contract the README advertises.
type RawJSONProvider interface {
	RawJSON() []byte
}

// rawIndented returns v's captured response body re-indented to match the
// formatter's two-space style, and true when v carries usable raw bytes.
// Malformed raw (shouldn't happen for a decoded API body) falls back to typed
// encoding by returning ok=false.
func rawIndented(v any) ([]byte, bool) {
	p, ok := v.(RawJSONProvider)
	if !ok {
		return nil, false
	}
	raw := p.RawJSON()
	if len(raw) == 0 {
		return nil, false
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

// JSON pretty-prints any value as two-space-indented JSON with a trailing
// newline. On a TTY (and NO_COLOR unset) output is colorized jq-style;
// piped/redirected/NO_COLOR output stays plain.
type JSON struct {
	W io.Writer
}

// Print writes v as JSON. Values carrying a raw response body are emitted
// verbatim (re-indented); everything else is encoded from the typed value.
func (j *JSON) Print(v any) error {
	out, ok := rawIndented(v)
	if ok {
		out = append(out, '\n')
	} else {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			return err
		}
		out = buf.Bytes()
	}
	if !ui.IsTTY(j.W) {
		_, err := j.W.Write(out)
		return err
	}
	_, err := j.W.Write(colorizeJSON(out))
	return err
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
