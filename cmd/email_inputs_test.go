package cmd

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// withStdinSource swaps stdinSource for the duration of the test, restoring
// the previous value on cleanup.
func withStdinSource(t *testing.T, body string, piped bool) {
	t.Helper()
	prev := stdinSource
	stdinSource = func() (io.Reader, bool) {
		return strings.NewReader(body), piped
	}
	t.Cleanup(func() { stdinSource = prev })
}

func TestCollectEmails_SingleLiteral(t *testing.T) {
	got, err := collectEmails([]string{"foo@bar.com"}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"foo@bar.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

// Comma-separated input is intentionally NOT split. Each CLI arg must look
// like a single email; a comma-joined string has two @s and so fails the
// shape check, surfacing as an arg error rather than being silently
// submitted to the API as one address.
func TestCollectEmails_CommaRejected(t *testing.T) {
	_, err := collectEmails([]string{"a@x.com,b@y.com"}, "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not a valid email") {
		t.Errorf("unexpected error: %v", err)
	}
}

// Garbage args (not email-shaped, not an existing file) should be rejected
// up front instead of being passed to the API as if they were addresses.
func TestCollectEmails_NonEmailNonFileRejected(t *testing.T) {
	_, err := collectEmails([]string{"hello"}, "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not a valid email") {
		t.Errorf("unexpected error: %v", err)
	}
}

// File-shaped args that don't actually exist on disk get a "file not found"
// error rather than the generic "not a valid email" message — the user's
// intent is clearly a file, so the error should point at that.
func TestCollectEmails_MissingFileRejected(t *testing.T) {
	_, err := collectEmails([]string{"missing.csv"}, "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "file not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLooksLikeEmail(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"foo@bar.com", true},
		{"a.b+tag@sub.example.co", true},
		{"foo", false},
		{"foo@bar", false},
		{"foo@@bar.com", false},
		{"a@x.com,b@y.com", false},
		{"emails.csv", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := looksLikeEmail(tc.in); got != tc.want {
			t.Errorf("looksLikeEmail(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// Multiple positional args (space-separated by the shell) — each becomes a
// single email entry, deduplicated.
func TestCollectEmails_SpaceSeparated(t *testing.T) {
	got, err := collectEmails([]string{"a@x.com", "b@y.com", "a@x.com"}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a@x.com", "b@y.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCollectEmails_CSVSingleColumn(t *testing.T) {
	p := writeTemp(t, "in.csv", "email\nfoo@bar.com\nbaz@qux.com\n")
	got, err := collectEmails([]string{p}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"foo@bar.com", "baz@qux.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestCollectEmails_CSVEmailColumnAutoDetect(t *testing.T) {
	p := writeTemp(t, "in.csv", "name,Email,age\nAlice,a@x.com,30\nBob,b@y.com,25\n")
	got, err := collectEmails([]string{p}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a@x.com", "b@y.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestCollectEmails_CSVMultiColumnNoFieldError(t *testing.T) {
	p := writeTemp(t, "in.csv", "name,address,age\nAlice,a@x.com,30\n")
	_, err := collectEmails([]string{p}, "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "specify --field") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCollectEmails_CSVMultiColumnWithField(t *testing.T) {
	p := writeTemp(t, "in.csv", "name,address,age\nAlice,a@x.com,30\nBob,b@y.com,25\n")
	got, err := collectEmails([]string{p}, "address")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a@x.com", "b@y.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestCollectEmails_JSONArrayOfStrings(t *testing.T) {
	p := writeTemp(t, "in.json", `["a@x.com","b@y.com"]`)
	got, err := collectEmails([]string{p}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a@x.com", "b@y.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestCollectEmails_JSONArrayOfObjectsWithField(t *testing.T) {
	p := writeTemp(t, "in.json", `[{"email":"a@x.com"},{"email":"b@y.com"}]`)
	got, err := collectEmails([]string{p}, "email")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a@x.com", "b@y.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestCollectEmails_EmptyError(t *testing.T) {
	_, err := collectEmails([]string{""}, "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not a valid email") {
		t.Errorf("unexpected error: %v", err)
	}
}

// `-` reads newline-delimited emails from stdin, treated as a .txt file.
func TestCollectEmails_StdinHappyPath(t *testing.T) {
	withStdinSource(t, "a@x.com\nb@y.com\n", true)
	got, err := collectEmails([]string{"-"}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a@x.com", "b@y.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

// `--field` is irrelevant for stdin (txt format) and must NOT error when
// supplied alongside `-`.
func TestCollectEmails_StdinIgnoresField(t *testing.T) {
	withStdinSource(t, "a@x.com\nb@y.com\n", true)
	got, err := collectEmails([]string{"-"}, "email")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a@x.com", "b@y.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

// Passing `-` while stdin is a TTY (no pipe) must surface a clear error
// instead of hanging waiting for input.
func TestCollectEmails_StdinTTYError(t *testing.T) {
	withStdinSource(t, "", false)
	_, err := collectEmails([]string{"-"}, "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no input piped") {
		t.Errorf("unexpected error: %v", err)
	}
}

// Mixing `-` with a literal email arg (cat-style) reads stdin AND appends
// the literal, deduped in first-seen order.
func TestCollectEmails_StdinMixedWithLiteral(t *testing.T) {
	withStdinSource(t, "a@x.com\nb@y.com\n", true)
	got, err := collectEmails([]string{"-", "c@z.com"}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a@x.com", "b@y.com", "c@z.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

// `-` may appear at most once — stdin can only be read once.
func TestCollectEmails_StdinTwiceRejected(t *testing.T) {
	withStdinSource(t, "a@x.com\n", true)
	_, err := collectEmails([]string{"-", "-"}, "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "only be used once") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLooksLikeBatchInput(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"foo@bar.com", false},
		{"foo", false},
		// Commas are NOT a batch signal anymore — verify's RunE has a
		// separate, dedicated check that emits a clearer error pointing
		// users at space-separated args / .csv files.
		{"a@x.com,b@y.com", false},
		{"emails.csv", true},
		{"data.JSON", true},
		{"list.txt", true},
		{"./emails.csv", true},
		{"path/to/file", true},
		{`C:\Users\foo\emails.csv`, true},
	}
	for _, tc := range cases {
		if got := looksLikeBatchInput(tc.in); got != tc.want {
			t.Errorf("looksLikeBatchInput(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

const bom = "\xef\xbb\xbf"

func TestCollectEmails_DedupeCaseInsensitive(t *testing.T) {
	got, err := collectEmails([]string{"A@x.com", "a@x.com", "a@X.COM"}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"A@x.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

// Excel prefixes CSV exports with a BOM, which would otherwise glue onto the
// `email` header and fail the column match.
func TestCollectEmails_CSVWithBOM(t *testing.T) {
	p := writeTemp(t, "in.csv", bom+"email,name\na@x.com,Alice\n")
	got, err := collectEmails([]string{p}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a@x.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestCollectEmails_JSONWithBOM(t *testing.T) {
	p := writeTemp(t, "in.json", bom+`["a@x.com"]`)
	got, err := collectEmails([]string{p}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a@x.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestCollectEmails_TXTWithBOM(t *testing.T) {
	p := writeTemp(t, "in.txt", bom+"a@x.com\nb@y.com\n")
	got, err := collectEmails([]string{p}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a@x.com", "b@y.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestCollectEmails_StdinWithBOM(t *testing.T) {
	withStdinSource(t, bom+"a@x.com\n", true)
	got, err := collectEmails([]string{"-"}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a@x.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

// A one-column CSV whose first row is an address has no header, so that row
// must not be dropped.
func TestCollectEmails_CSVHeaderless(t *testing.T) {
	p := writeTemp(t, "in.csv", "a@x.com\nb@y.com\n")
	got, err := collectEmails([]string{p}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a@x.com", "b@y.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

// With --field, row 1 is always a header even if it looks like an address.
func TestCollectEmails_CSVFieldAlwaysHeader(t *testing.T) {
	p := writeTemp(t, "in.csv", "a@x.com\nb@y.com\n")
	got, err := collectEmails([]string{p}, "a@x.com")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"b@y.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestCollectEmails_CSVRaggedRows(t *testing.T) {
	p := writeTemp(t, "in.csv", "name,email\nAlice,a@x.com,extra\nBob\nCarol,c@z.com\n")
	got, err := collectEmails([]string{p}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a@x.com", "c@z.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestCollectEmails_JSON(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		field string
		want  []string
	}{
		{"array of objects auto", `[{"Email":"a@x.com"},{"email":"b@y.com"}]`, "", []string{"a@x.com", "b@y.com"}},
		{"wrapped strings auto", `{"emails":["a@x.com"],"count":1}`, "", []string{"a@x.com"}},
		{"wrapped objects auto", `{"contacts":[{"email":"a@x.com"}]}`, "", []string{"a@x.com"}},
		{"wrapped objects single segment", `{"contacts":[{"email":"a@x.com"}]}`, "email", []string{"a@x.com"}},
		{"dotted path", `{"contacts":[{"email":"a@x.com"}],"tags":["x"]}`, "contacts.email", []string{"a@x.com"}},
		{"wrapper key to strings", `{"contacts":["a@x.com"],"tags":["x"]}`, "contacts", []string{"a@x.com"}},
		{"nested path", `{"data":{"users":[{"profile":{"email":"a@x.com"}},{"profile":{"email":"b@y.com"}}]}}`, "data.users.profile.email", []string{"a@x.com", "b@y.com"}},
		{"path ending in string array", `[{"emails":["a@x.com","b@y.com"]},{"emails":["c@z.com"]}]`, "emails", []string{"a@x.com", "b@y.com", "c@z.com"}},
		{"non-string leaves skipped", `[{"email":"a@x.com"},{"email":5},{"email":null}]`, "email", []string{"a@x.com"}},
		{"array of strings ignores field", `["a@x.com"]`, "email", []string{"a@x.com"}},
		{"literal dotted key", `[{"contact.email":"a@x.com"}]`, "contact.email", []string{"a@x.com"}},
		{"wrapped literal dotted key", `{"rows":[{"contact.email":"a@x.com"}]}`, "contact.email", []string{"a@x.com"}},
		{"case variants pick sorted first", `[{"Email":"a@x.com","EMAIL":"b@y.com"}]`, "", []string{"b@y.com"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeTemp(t, "in.json", tc.body)
			got, err := collectEmails([]string{p}, tc.field)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestCollectEmails_JSONErrors(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		field string
		want  string
	}{
		{"multiple arrays", `{"a":["a@x.com"],"b":["b@y.com"]}`, "", "--field <path>"},
		{"no email key", `[{"address":"a@x.com"}]`, "", "--field <path>"},
		{"missing path", `{"contacts":[{"email":"a@x.com"}],"tags":["x"]}`, "contacts.mail", `field "contacts.mail" not found`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeTemp(t, "in.json", tc.body)
			_, err := collectEmails([]string{p}, tc.field)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
