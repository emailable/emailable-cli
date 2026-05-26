package output

import (
	"encoding/csv"
	"encoding/json"
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
	if rows[0][0] != "email" || rows[0][1] != "state" || rows[0][2] != "score" {
		t.Errorf("unexpected header: %v", rows[0])
	}
	if rows[1][0] != "a@x.com" || rows[1][1] != "deliverable" || rows[1][2] != "100" {
		t.Errorf("unexpected row 1: %v", rows[1])
	}
	// Booleans render as true/false strings.
	if rows[1][8] != "true" { // free
		t.Errorf("expected free=true, got %q", rows[1][8])
	}
	if rows[2][5] != "true" { // disposable
		t.Errorf("expected disposable=true, got %q", rows[2][5])
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
