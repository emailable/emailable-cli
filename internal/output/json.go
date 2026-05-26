package output

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/emailable/emailable-cli/internal/ui"
)

// JSON pretty-prints any value as JSON, two-space indented, with a trailing
// newline. When the writer is a TTY (and NO_COLOR isn't set) output is
// colorized jq-style: keys, strings, numbers, booleans, and null each get a
// distinct color. Piped, redirected, or NO_COLOR=1 output stays plain so
// `--json | jq`, file capture, and CI logs are unaffected.
type JSON struct {
	W io.Writer
}

// Print encodes v to JSON and writes it.
func (j *JSON) Print(v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	if !ui.IsTTY(j.W) {
		_, err := j.W.Write(buf.Bytes())
		return err
	}
	_, err := j.W.Write(colorizeJSON(buf.Bytes()))
	return err
}

// ANSI escape sequences for the JSON palette. We emit raw codes rather than
// going through lipgloss because the global lipgloss renderer probes the
// process's stdin/out/err at init time and silently drops to ASCII output
// when it can't confirm color support — which means a TTY-detected writer
// can still receive uncolored bytes through Style.Render. Raw codes are
// deterministic, easy to test, and match how internal/ui/style.go already
// colors the help screen.
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
			// Look ahead past horizontal whitespace for ':' to decide whether
			// this string is an object key or a value. Newlines don't appear
			// between a key and its colon in encoding/json's output, so we
			// only skip spaces / tabs.
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
