package output

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// rawDoc exercises the raw-passthrough path that real API responses take.
type rawDoc struct{ raw []byte }

func (r rawDoc) RawJSON() []byte { return r.raw }

func mustQuery(t *testing.T, expr string) *Query {
	t.Helper()
	q, err := CompileQuery(expr)
	if err != nil {
		t.Fatalf("CompileQuery(%q): %v", expr, err)
	}
	return q
}

func printWith(t *testing.T, expr string, v any) string {
	t.Helper()
	var buf bytes.Buffer
	j := &JSON{W: &buf, Query: mustQuery(t, expr)}
	if err := j.Print(v); err != nil {
		t.Fatalf("Print: %v", err)
	}
	return buf.String()
}

func TestJQ_StringResultIsRaw(t *testing.T) {
	got := printWith(t, ".state", map[string]any{"state": "deliverable"})
	if got != "deliverable\n" {
		t.Errorf("got %q, want %q", got, "deliverable\n")
	}
}

func TestJQ_NonStringResultIsJSON(t *testing.T) {
	got := printWith(t, "{s: .state}", map[string]any{"state": "deliverable"})
	want := "{\n  \"s\": \"deliverable\"\n}\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestJQ_MultipleResults(t *testing.T) {
	got := printWith(t, ".[]", []any{"a", "b", "c"})
	if got != "a\nb\nc\n" {
		t.Errorf("got %q, want %q", got, "a\nb\nc\n")
	}
}

func TestJQ_NoResults(t *testing.T) {
	got := printWith(t, ".[] | select(. > 10)", []any{1, 2, 3})
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestJQ_RawPassthrough(t *testing.T) {
	doc := rawDoc{raw: []byte(`{"unmodeled":{"nested":"kept"}}`)}
	got := printWith(t, ".unmodeled.nested", doc)
	if got != "kept\n" {
		t.Errorf("got %q, want %q", got, "kept\n")
	}
}

func TestJQ_RuntimeErrorSurfaces(t *testing.T) {
	var buf bytes.Buffer
	j := &JSON{W: &buf, Query: mustQuery(t, ".foo")}
	if err := j.Print(42); err == nil {
		t.Fatal("expected an error indexing a number, got nil")
	}
}

func TestJQ_NoHTMLEscaping(t *testing.T) {
	got := printWith(t, ".", map[string]any{"url": "https://x/?a=1&b=2"})
	if !strings.Contains(got, "a=1&b=2") {
		t.Errorf("expected unescaped ampersand in output, got %q", got)
	}
}

func TestCompileQuery_BadExpr(t *testing.T) {
	if _, err := CompileQuery(".["); err == nil {
		t.Fatal("expected compile error for malformed expression")
	}
}

func TestJQ_FilterError(t *testing.T) {
	var buf bytes.Buffer
	// .emails is absent (null); iterating null is a runtime error.
	j := &JSON{W: &buf, Query: mustQuery(t, ".emails[]")}
	err := j.Print(map[string]any{"event": "progress"})
	var fe *FilterError
	if !errors.As(err, &fe) {
		t.Fatalf("expected *FilterError, got %T: %v", err, err)
	}
}

func TestCompact_Unfiltered(t *testing.T) {
	var buf bytes.Buffer
	j := &JSON{W: &buf, Compact: true}
	if err := j.Print(map[string]any{"event": "progress", "processed": 1}); err != nil {
		t.Fatalf("print: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "\n  ") {
		t.Errorf("compact output should not be indented:\n%q", got)
	}
	if got != `{"event":"progress","processed":1}`+"\n" {
		t.Errorf("got %q", got)
	}
}

func TestCompact_FilteredObject(t *testing.T) {
	var buf bytes.Buffer
	j := &JSON{W: &buf, Compact: true, Query: mustQuery(t, "{e: .event}")}
	if err := j.Print(map[string]any{"event": "complete"}); err != nil {
		t.Fatalf("print: %v", err)
	}
	if got := buf.String(); got != `{"e":"complete"}`+"\n" {
		t.Errorf("got %q", got)
	}
}
