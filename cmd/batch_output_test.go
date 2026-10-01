package cmd

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBatchGet_WaitPartialBeforeAuth(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler()) // not logged in
	res := runRoot(t, "batch", "get", "bch_1", "--wait", "--partial")
	assertInvalidInput(t, res)
}

func TestBatchGet_OutputUnfinishedBatch(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"message": "Your batch is being processed.", "processed": 1, "total": 4})
	}))
	env.seedAPIKey(t, "sk_test_xxx")
	out := filepath.Join(t.TempDir(), "results.csv")

	res := runRoot(t, "batch", "get", "bch_1", "-o", out)
	assertInvalidInput(t, res)
	if !strings.Contains(res.Err.Error(), "batch bch_1 is still verifying (1/4); use --wait or --partial") {
		t.Errorf("unexpected message: %q", res.Err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("no file should be written for an unfinished batch")
	}
}

func TestBatchGet_OutputPartial(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("partial") != "true" {
			t.Errorf("expected partial=true, got %q", r.URL.RawQuery)
		}
		writeJSON(w, map[string]any{
			"id":           "bch_1",
			"message":      "Your batch is being processed.",
			"emails":       []map[string]any{{"email": "a@x.com", "state": "deliverable"}},
			"total_counts": map[string]int{"processed": 1, "total": 4},
		})
	}))
	env.seedAPIKey(t, "sk_test_xxx")
	out := filepath.Join(t.TempDir(), "results.csv")

	res := runRoot(t, "batch", "get", "bch_1", "--partial", "-o", out)
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	rows := readCSVFile(t, out)
	if len(rows) != 2 || rows[1][0] != "a@x.com" {
		t.Errorf("unexpected rows: %v", rows)
	}
	if !strings.Contains(res.Stderr.String(), "Saved 1 result to") {
		t.Errorf("expected success line, got %q", res.Stderr.String())
	}
}

func TestBatchGet_OutputDownloadFile(t *testing.T) {
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	f, _ := zw.Create("bch_big.csv")
	_, _ = f.Write([]byte("email,state,score\na@x.com,deliverable,99\nb@y.com,risky,50\nc@z.com,unknown,0\n"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	var downloadAuth string
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloadAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "binary/octet-stream")
		_, _ = w.Write(zbuf.Bytes())
	}))
	t.Cleanup(storage.Close)

	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"id":            "bch_big",
			"message":       "Batch verification completed.",
			"download_file": storage.URL + "/bch_big.csv?X-Amz-Signature=x",
			"total_counts":  map[string]int{"processed": 3, "total": 3},
		})
	}))
	env.seedAPIKey(t, "sk_test_xxx")
	out := filepath.Join(t.TempDir(), "results.csv")

	res := runRoot(t, "batch", "get", "bch_big", "-o", out)
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	if downloadAuth != "" {
		t.Errorf("Authorization leaked to storage host: %q", downloadAuth)
	}
	rows := readCSVFile(t, out)
	if len(rows) != 4 || rows[1][0] != "a@x.com" || rows[3][0] != "c@z.com" {
		t.Errorf("unexpected rows: %v", rows)
	}
	if !strings.Contains(res.Stderr.String(), "Saved 3 results to") {
		t.Errorf("expected row count in success line, got %q", res.Stderr.String())
	}
}

func TestBatchGet_DownloadFileHint(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"id": "bch_big", "download_file": "https://cdn.example/x.csv"})
	}))
	env.seedAPIKey(t, "sk_test_xxx")

	res := runRoot(t, "batch", "get", "bch_big")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	if !strings.Contains(res.Stdout.String(), "emailable batch get bch_big -o results.csv") {
		t.Errorf("expected save hint, got %q", res.Stdout.String())
	}
}

func TestBatchGet_OutputExpiredResults(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"id":           "bch_old",
			"message":      "Batch verification completed.",
			"total_counts": map[string]int{"processed": 2, "total": 2},
		})
	}))
	env.seedAPIKey(t, "sk_test_xxx")
	out := filepath.Join(t.TempDir(), "results.csv")

	res := runRoot(t, "batch", "get", "bch_old", "-o", out)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "no per-email results") {
		t.Fatalf("expected no-results error, got %v", res.Err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("no file should be written")
	}
}

func readCSVFile(t *testing.T, path string) [][]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
