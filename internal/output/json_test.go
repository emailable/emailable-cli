package output

import (
	"bytes"
	"strings"
	"testing"
)

// TestJSON_PlainOnNonTTY verifies the buffer path emits exactly the same
// bytes encoding/json would have written: no ANSI codes, trailing newline,
// two-space indent. This is the contract for `--json | jq` and any CI log
// capture — colorization must be strictly TTY-gated.
func TestJSON_PlainOnNonTTY(t *testing.T) {
	var buf bytes.Buffer
	j := &JSON{W: &buf}
	if err := j.Print(map[string]any{"a": 1, "b": "two", "c": true, "d": nil}); err != nil {
		t.Fatalf("print: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "\x1b[") {
		t.Errorf("expected no ANSI escapes when writing to a non-TTY buffer:\n%q", got)
	}
	for _, want := range []string{`"a": 1`, `"b": "two"`, `"c": true`, `"d": null`} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output:\n%s", want, got)
		}
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("expected trailing newline, got %q", got)
	}
}

// TestScanString covers the escape-aware string scanner used by the JSON
// colorizer. A naive scanner that bails on the first '"' would split
// strings containing escaped quotes — these cases pin that behavior.
func TestScanString(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{`"abc"`, 5},
		{`""`, 2},
		{`"he said \"hi\""`, 16},
		{`"back\\slash"`, 13},
		{`"unicode é e-acute"`, 20}, // 'é' is two UTF-8 bytes; we count bytes, not runes.
	}
	for _, c := range cases {
		got := scanString([]byte(c.in), 0)
		if got != c.want {
			t.Errorf("scanString(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestScanNumber covers the number scanner — the accepted character set is
// intentionally a superset (encoding/json only produces well-formed numbers
// so we don't bother validating), but it must at least walk all the bytes
// in any valid JSON number form.
func TestScanNumber(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"0", 1},
		{"42", 2},
		{"-7", 2},
		{"3.14", 4},
		{"1e10", 4},
		{"1.5E-3", 6},
		{"-0.1e+2", 7},
	}
	for _, c := range cases {
		got := scanNumber([]byte(c.in), 0)
		if got != c.want {
			t.Errorf("scanNumber(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestColorizeJSON_RoundTripsContent verifies the colorizer doesn't drop or
// mangle any bytes from the source — when ANSI styling is stripped, the
// output should match the input verbatim.
func TestColorizeJSON_RoundTripsContent(t *testing.T) {
	src := []byte(`{
  "name": "jarrett",
  "tagline": "he said \"hi\"",
  "score": 100,
  "ratio": -1.5e-3,
  "ok": true,
  "nope": false,
  "missing": null,
  "nested": {"x": [1, 2, 3]}
}
`)
	out := colorizeJSON(src)
	stripped := stripANSI(out)
	if !bytes.Equal(stripped, src) {
		t.Errorf("colorizeJSON altered content.\n--- want ---\n%s\n--- got (ANSI-stripped) ---\n%s", src, stripped)
	}
}

// TestColorizeJSON_TokenStyling pins the specific ANSI prefix used for each
// kind of token so palette changes are deliberate. We use raw escapes
// (rather than going through lipgloss) so this is deterministic regardless
// of the host environment.
func TestColorizeJSON_TokenStyling(t *testing.T) {
	src := []byte("{\n  \"name\": \"jarrett\",\n  \"score\": 100,\n  \"ok\": true,\n  \"nope\": false,\n  \"missing\": null\n}\n")
	out := string(colorizeJSON(src))

	cases := []struct {
		desc   string
		want   string
		prefix string
	}{
		{"key", jsonAnsiKey + `"name"` + jsonAnsiReset, jsonAnsiKey},
		{"string value", jsonAnsiString + `"jarrett"` + jsonAnsiReset, jsonAnsiString},
		{"number", jsonAnsiNumber + "100" + jsonAnsiReset, jsonAnsiNumber},
		{"true", jsonAnsiBool + "true" + jsonAnsiReset, jsonAnsiBool},
		{"false", jsonAnsiBool + "false" + jsonAnsiReset, jsonAnsiBool},
		{"null", jsonAnsiNull + "null" + jsonAnsiReset, jsonAnsiNull},
	}
	for _, tc := range cases {
		if !strings.Contains(out, tc.want) {
			t.Errorf("%s: expected %q in output:\n%s", tc.desc, tc.want, out)
		}
	}

	// Structural punctuation should survive intact (no leading/trailing
	// escapes attached to braces / commas / colons).
	for _, raw := range []string{": ", ",\n", "{\n", "\n}"} {
		if !strings.Contains(out, raw) {
			t.Errorf("expected raw punctuation %q to survive coloring intact:\n%s", raw, out)
		}
	}
}

// stripANSI removes CSI escape sequences so a colored payload can be
// compared against its plain source.
func stripANSI(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); {
		if b[i] == 0x1b && i+1 < len(b) && b[i+1] == '[' {
			i += 2
			for i < len(b) && b[i] != 'm' {
				i++
			}
			if i < len(b) {
				i++ // skip the terminating 'm'
			}
			continue
		}
		out = append(out, b[i])
		i++
	}
	return out
}
