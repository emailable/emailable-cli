package output

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emailable/emailable-cli/internal/api"
)

func sampleBatch() *api.BatchStatus {
	return &api.BatchStatus{
		ID:    "batch-1",
		Total: 3,
		Emails: []api.VerifyResult{
			{Email: "a@x.com", State: "deliverable", Score: 100, Domain: "x.com", Free: true},
			{Email: "b@y.com", State: "undeliverable", Score: 0, Domain: "y.com", Disposable: true},
			{Email: "c@z.com", State: "risky", Score: 50, Domain: "z.com", AcceptAll: true, MXRecord: "mx.z.com"},
		},
	}
}

func TestWriteResults_BatchCSV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.csv")
	n, err := WriteResults(sampleBatch(), SaveOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("count: got %d want 3", n)
	}

	// .tmp must not linger.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf(".tmp file should not exist after success: err=%v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r := csv.NewReader(strings.NewReader(string(data)))
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 { // header + 3
		t.Fatalf("got %d rows, want 4", len(rows))
	}
	if strings.Join(rows[0], ",") != strings.Join(CSVColumns, ",") {
		t.Errorf("unexpected header: %v", rows[0])
	}
	col := columnIndex(rows[0])
	if rows[1][col["email"]] != "a@x.com" || rows[1][col["state"]] != "deliverable" || rows[1][col["score"]] != "100" {
		t.Errorf("unexpected row 1: %v", rows[1])
	}
	// Booleans render as true/false strings.
	if rows[1][col["free"]] != "true" {
		t.Errorf("expected free=true, got %q", rows[1][col["free"]])
	}
	if rows[2][col["disposable"]] != "true" {
		t.Errorf("expected disposable=true, got %q", rows[2][col["disposable"]])
	}
}

func columnIndex(header []string) map[string]int {
	m := make(map[string]int, len(header))
	for i, h := range header {
		m[h] = i
	}
	return m
}

// fetchViaAPI runs body through the real API client so the result carries
// its raw JSON, as it does in the CLI.
func fetchViaAPI(t *testing.T, body string) (*api.BatchStatus, *api.VerifyResult) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c := api.New(srv.URL, "k", nil)
	b, err := c.Batch(context.Background(), "bch_1", false)
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.Verify(context.Background(), "a@x.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	return b, v
}

func readCSV(t *testing.T, path string) [][]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestWriteResults_CSVBlankForAbsentAndNull(t *testing.T) {
	// As with --response-fields email,state,free: other fields are absent.
	b, _ := fetchViaAPI(t, `{"id":"bch_1","emails":[
		{"email":"a@x.com","state":"deliverable","free":false,"score":0,"tag":null,"duration":0.25},
		{"email":"b@y.com","state":"risky"}
	]}`)
	path := filepath.Join(t.TempDir(), "out.csv")
	n, err := WriteResults(b, SaveOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("count: got %d want 2", n)
	}
	rows := readCSV(t, path)
	col := columnIndex(rows[0])
	cases := []struct {
		row       int
		col, want string
	}{
		{1, "free", "false"},
		{1, "score", "0"},
		{1, "tag", ""},
		{1, "duration", "0.25"},
		{1, "disposable", ""},
		{2, "free", ""},
		{2, "score", ""},
		{2, "state", "risky"},
	}
	for _, tc := range cases {
		if got := rows[tc.row][col[tc.col]]; got != tc.want {
			t.Errorf("row %d %s: got %q want %q", tc.row, tc.col, got, tc.want)
		}
	}
}

func TestWriteResults_CSVAppendsUnknownKeysSorted(t *testing.T) {
	_, v := fetchViaAPI(t, `{"email":"a@x.com","state":"deliverable","zeta":"z","alpha":{"k":1}}`)
	path := filepath.Join(t.TempDir(), "one.csv")
	if _, err := WriteResults(v, SaveOptions{Path: path}); err != nil {
		t.Fatal(err)
	}
	rows := readCSV(t, path)
	want := append(append([]string(nil), CSVColumns...), "alpha", "zeta")
	if strings.Join(rows[0], ",") != strings.Join(want, ",") {
		t.Fatalf("header: got %v want %v", rows[0], want)
	}
	col := columnIndex(rows[0])
	if got := rows[1][col["alpha"]]; got != `{"k":1}` {
		t.Errorf("alpha: got %q", got)
	}
	if got := rows[1][col["zeta"]]; got != "z" {
		t.Errorf("zeta: got %q", got)
	}
}

func TestWriteResults_BatchJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")
	n, err := WriteResults(sampleBatch(), SaveOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("count: got %d want 3", n)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Must be pretty-printed (contains newline + indent).
	if !strings.Contains(string(data), "\n  ") {
		t.Errorf("expected indented JSON, got: %s", data)
	}
	var got api.BatchStatus
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != "batch-1" || len(got.Emails) != 3 {
		t.Errorf("unexpected parsed batch: %+v", got)
	}
}

func TestWriteResults_UnknownExtensionFallsBackToJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	stderr, err := os.CreateTemp(dir, "stderr-*")
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()

	if _, err := WriteResults(sampleBatch(), SaveOptions{Path: path, Stderr: stderr}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got api.BatchStatus
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("expected JSON content for .txt fallback, parse failed: %v\ncontent: %s", err, data)
	}
	if got.ID != "batch-1" {
		t.Errorf("got %+v", got)
	}

	// Note should be written to stderr.
	if _, err := stderr.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	noteBytes, _ := os.ReadFile(stderr.Name())
	if !strings.Contains(string(noteBytes), "unrecognized extension") {
		t.Errorf("expected stderr note, got: %q", noteBytes)
	}
}

func TestWriteResults_ForceJSONOverridesCSVExtension(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.csv")
	if _, err := WriteResults(sampleBatch(), SaveOptions{Path: path, ForceJSON: true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Parsing as JSON should succeed; parsing as CSV would NOT yield a
	// VerifyResult struct.
	var got api.BatchStatus
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("expected JSON content despite .csv ext, parse failed: %v\ncontent: %s", err, data)
	}
	if got.ID != "batch-1" {
		t.Errorf("got %+v", got)
	}
}

func TestWriteResults_FileMode0644(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"out.csv", "out.json"} {
		path := filepath.Join(dir, name)
		if _, err := WriteResults(sampleBatch(), SaveOptions{Path: path}); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		// On Unix, mode is the low bits. Umask can clear bits but not add
		// them, so we check that no bits beyond 0644 are set.
		mode := info.Mode().Perm()
		if mode&^0o644 != 0 {
			t.Errorf("%s: mode %o has bits beyond 0644", name, mode)
		}
	}
}

func TestWriteResults_SingleVerifyResultJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "single.json")
	single := &api.VerifyResult{Email: "a@x.com", State: "deliverable", Score: 99}
	n, err := WriteResults(single, SaveOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("count: got %d want 1", n)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Must be a JSON object, not an array.
	trimmed := strings.TrimSpace(string(data))
	if !strings.HasPrefix(trimmed, "{") {
		t.Errorf("expected JSON object, got: %s", trimmed)
	}
	var got api.VerifyResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Email != "a@x.com" || got.State != "deliverable" || got.Score != 99 {
		t.Errorf("unexpected single: %+v", got)
	}
}

func TestWriteResults_SingleVerifyResultCSV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "single.csv")
	single := &api.VerifyResult{Email: "a@x.com", State: "deliverable", Score: 99, Domain: "x.com"}
	n, err := WriteResults(single, SaveOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("count: got %d want 1", n)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 { // header + 1
		t.Fatalf("got %d rows want 2", len(rows))
	}
	if rows[1][0] != "a@x.com" {
		t.Errorf("unexpected row: %v", rows[1])
	}
}

func TestWriteResults_UnsupportedShapeCSVFallsBackToJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "acct.csv")
	stderr, err := os.CreateTemp(dir, "stderr-*")
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	acct := &api.Account{OwnerEmail: "me@x.com", AvailableCredits: 42}
	if _, err := WriteResults(acct, SaveOptions{Path: path, Stderr: stderr}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got api.Account
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("expected JSON for unsupported CSV shape: %v\ncontent: %s", err, data)
	}
	if got.OwnerEmail != "me@x.com" {
		t.Errorf("got %+v", got)
	}
	noteBytes, _ := os.ReadFile(stderr.Name())
	if !strings.Contains(string(noteBytes), "not supported for CSV") {
		t.Errorf("expected fallback note, got: %q", noteBytes)
	}
}

func TestWriteResults_EmptyPathError(t *testing.T) {
	_, err := WriteResults(sampleBatch(), SaveOptions{})
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestWriteResults_SliceOfResultsCSV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "slice.csv")
	rs := []api.VerifyResult{
		{Email: "a@x.com", State: "deliverable"},
		{Email: "b@y.com", State: "risky"},
	}
	n, err := WriteResults(rs, SaveOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("count: got %d want 2", n)
	}
	data, _ := os.ReadFile(path)
	rows, _ := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if len(rows) != 3 {
		t.Errorf("got %d rows want 3", len(rows))
	}
}
