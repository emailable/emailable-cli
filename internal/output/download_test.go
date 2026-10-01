package output

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emailable/emailable-cli/internal/api"
)

const downloadCSV = "email,state,score,free,duration,extra_col\n" +
	"a@x.com,deliverable,95,true,0.5,foo\n" +
	"b@y.com,undeliverable,0,,,\n"

func zipped(t *testing.T, name, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func downloadBatch(t *testing.T) *api.BatchStatus {
	b, _ := fetchViaAPI(t, `{"id":"bch_1","message":"Batch verification completed.","download_file":"https://cdn.example/bch_1.csv?sig=x","total_counts":{"processed":2,"total":2}}`)
	return b
}

func TestWriteDownload_ZipToCSV(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.csv")
	d := &api.Download{URL: "https://cdn.example/bch_1.csv", ContentType: "application/zip", Body: zipped(t, "bch_1.csv", downloadCSV)}
	n, err := WriteDownload(d, downloadBatch(t), SaveOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("count: got %d want 2", n)
	}
	rows := readCSV(t, path)
	want := append(append([]string(nil), CSVColumns...), "extra_col")
	if strings.Join(rows[0], ",") != strings.Join(want, ",") {
		t.Fatalf("header: got %v", rows[0])
	}
	col := columnIndex(rows[0])
	if rows[1][col["email"]] != "a@x.com" || rows[1][col["score"]] != "95" || rows[1][col["free"]] != "true" || rows[1][col["extra_col"]] != "foo" {
		t.Errorf("row 1: %v", rows[1])
	}
	if rows[2][col["free"]] != "" || rows[2][col["reason"]] != "" {
		t.Errorf("row 2 should have blanks: %v", rows[2])
	}
}

func TestWriteDownload_PlainCSVToJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.json")
	d := &api.Download{URL: "https://cdn.example/bch_1.csv", ContentType: "text/csv", Body: []byte(downloadCSV)}
	n, err := WriteDownload(d, downloadBatch(t), SaveOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("count: got %d want 2", n)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		ID     string           `json:"id"`
		Emails []map[string]any `json:"emails"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, data)
	}
	if doc.ID != "bch_1" || len(doc.Emails) != 2 {
		t.Fatalf("unexpected doc: %s", data)
	}
	first := doc.Emails[0]
	if first["score"] != float64(95) || first["free"] != true || first["duration"] != 0.5 || first["extra_col"] != "foo" {
		t.Errorf("types not restored: %v", first)
	}
	if v, ok := doc.Emails[1]["free"]; !ok || v != nil {
		t.Errorf("blank cell should be null, got %v (present=%v)", v, ok)
	}
}

func TestWriteDownload_UnrecognizedColumnsPassThrough(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.csv")
	body := "Address,Result\na@x.com,ok\n"
	d := &api.Download{URL: "https://cdn.example/x.csv", Body: []byte(body)}
	n, err := WriteDownload(d, downloadBatch(t), SaveOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("count: got %d want 1", n)
	}
	data, _ := os.ReadFile(path)
	if string(data) != body {
		t.Errorf("expected bytes as served, got %q", data)
	}
}

func TestWriteDownload_UnknownFormat(t *testing.T) {
	binary := []byte{0x00, 0x01, 0xff, 0xfe}
	dir := t.TempDir()

	// Extension mismatch: refuse rather than write garbage.
	d := &api.Download{URL: "https://cdn.example/x.bin", Body: binary}
	if _, err := WriteDownload(d, downloadBatch(t), SaveOptions{Path: filepath.Join(dir, "r.csv")}); err == nil || !strings.Contains(err.Error(), "unrecognized format") {
		t.Errorf("expected unrecognized format error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "r.csv")); !os.IsNotExist(err) {
		t.Errorf("no file should be written on error")
	}

	// Extension match: write as-is.
	d = &api.Download{URL: "https://cdn.example/x.csv", Body: binary}
	if _, err := WriteDownload(d, downloadBatch(t), SaveOptions{Path: filepath.Join(dir, "r.csv")}); err != nil {
		t.Fatalf("expected as-is write, got %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "r.csv"))
	if !bytes.Equal(data, binary) {
		t.Errorf("bytes changed: %v", data)
	}
}

func TestWriteDownload_JSONBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.csv")
	d := &api.Download{Body: []byte(`[{"email":"a@x.com","state":"deliverable","tag":null}]`), ContentType: "application/json"}
	n, err := WriteDownload(d, downloadBatch(t), SaveOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("count: got %d want 1", n)
	}
	rows := readCSV(t, path)
	col := columnIndex(rows[0])
	if rows[1][col["state"]] != "deliverable" || rows[1][col["tag"]] != "" {
		t.Errorf("row: %v", rows[1])
	}
}
