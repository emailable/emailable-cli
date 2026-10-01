package output

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/emailable/emailable-cli/internal/api"
)

// The API documents download_file as a ZIP compressed CSV, but its example
// URL ends in .csv, so the format is sniffed from the body rather than trusted.
const (
	formatCSV  = "csv"
	formatJSON = "json"
)

var (
	zipMagic = []byte("PK\x03\x04")
	utf8BOM  = []byte("\xef\xbb\xbf")
)

// Field types from the verify response docs, used to restore types when a
// downloaded CSV is converted to JSON.
var (
	boolFields  = map[string]bool{"accept_all": true, "disposable": true, "free": true, "mailbox_full": true, "no_reply": true, "role": true}
	intFields   = map[string]bool{"score": true, "birth_year": true}
	floatFields = map[string]bool{"duration": true}
)

// WriteDownload converts a batch's downloaded results file to the format
// opts.Path asks for (CSV or JSON) and writes it atomically. batch supplies
// the surrounding document for JSON output. Returns the row count.
func WriteDownload(d *api.Download, batch *api.BatchStatus, opts SaveOptions) (int, error) {
	if opts.Path == "" {
		return 0, fmt.Errorf("output path is required")
	}
	data, name, err := unzipDownload(d.Body, d.ContentType)
	if err != nil {
		return 0, err
	}
	if name == "" {
		name = urlPath(d.URL)
	}
	wantCSV := useCSV(opts)

	format := sniffFormat(data, d.ContentType)
	if format == "" {
		return writeUnknownDownload(data, name, wantCSV, opts.Path)
	}

	if wantCSV && format == formatCSV {
		return convertCSVDownload(data, opts.Path)
	}

	rows, err := parseDownload(data, format)
	if err != nil {
		return 0, err
	}
	if wantCSV {
		return writeCSV(rows, opts.Path)
	}

	doc, err := batchDocument(batch)
	if err != nil {
		return 0, err
	}
	doc["emails"] = rows
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("marshal json: %w", err)
	}
	if err := atomicWrite(opts.Path, append(out, '\n')); err != nil {
		return 0, err
	}
	return len(rows), nil
}

// unzipDownload returns the CSV inside a ZIP body, or body unchanged when it
// isn't a ZIP. name is the archive entry's name, when there is one.
func unzipDownload(body []byte, contentType string) ([]byte, string, error) {
	if !bytes.HasPrefix(body, zipMagic) && !strings.Contains(strings.ToLower(contentType), "zip") {
		return body, "", nil
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, "", fmt.Errorf("open downloaded zip: %w", err)
	}
	var files []*zip.File
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || strings.HasPrefix(f.Name, "__MACOSX/") {
			continue
		}
		files = append(files, f)
	}
	var pick *zip.File
	for _, f := range files {
		if strings.EqualFold(path.Ext(f.Name), ".csv") {
			pick = f
			break
		}
	}
	if pick == nil && len(files) == 1 {
		pick = files[0]
	}
	if pick == nil {
		return nil, "", fmt.Errorf("downloaded zip has no results file (%d entries)", len(files))
	}
	rc, err := pick.Open()
	if err != nil {
		return nil, "", fmt.Errorf("open %s in downloaded zip: %w", pick.Name, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, "", fmt.Errorf("read %s in downloaded zip: %w", pick.Name, err)
	}
	return data, pick.Name, nil
}

// sniffFormat returns formatCSV, formatJSON, or "" when the body is neither.
func sniffFormat(data []byte, contentType string) string {
	trimmed := bytes.TrimLeft(bytes.TrimPrefix(data, utf8BOM), " \t\r\n")
	if len(trimmed) == 0 {
		return ""
	}
	if trimmed[0] == '[' || trimmed[0] == '{' || strings.Contains(strings.ToLower(contentType), "json") {
		if json.Valid(trimmed) {
			return formatJSON
		}
		return ""
	}
	if !utf8.Valid(trimmed) || bytes.IndexByte(trimmed, 0) >= 0 {
		return ""
	}
	r := csv.NewReader(bytes.NewReader(trimmed))
	r.FieldsPerRecord = -1
	if _, err := r.Read(); err != nil {
		return ""
	}
	return formatCSV
}

// convertCSVDownload rewrites a downloaded CSV into the standard column
// layout one row at a time, so even a 1M-row export is never held in memory
// as parsed rows.
func convertCSVDownload(data []byte, dest string) (int, error) {
	r, header, err := downloadCSVReader(data)
	if err != nil {
		return 0, err
	}
	if !hasKnownColumn(header) {
		// Columns we don't recognize: keep the file exactly as served rather
		// than emit a sheet of blank known columns.
		n := 0
		for {
			if _, err := r.Read(); err == io.EOF {
				break
			} else if err != nil {
				return 0, fmt.Errorf("parse downloaded csv: %w", err)
			}
			n++
		}
		if err := atomicWrite(dest, data); err != nil {
			return 0, err
		}
		return n, nil
	}
	present := make(map[string]bool, len(header))
	for _, h := range header {
		present[h] = true
	}
	return writeCSVRows(dest, csvHeaderWith(present), csvDownloadRows(r, header))
}

// parseDownload reads every result row, for JSON output where the whole
// document has to be built anyway.
func parseDownload(data []byte, format string) ([]record, error) {
	data = bytes.TrimPrefix(data, utf8BOM)
	if format == formatJSON {
		return parseJSONDownload(data)
	}
	r, header, err := downloadCSVReader(data)
	if err != nil {
		return nil, err
	}
	next := csvDownloadRows(r, header)
	rows := []record{}
	for {
		rec, ok, err := next()
		if err != nil {
			return nil, err
		}
		if !ok {
			return rows, nil
		}
		rows = append(rows, rec)
	}
}

// downloadCSVReader returns a reader positioned after the header row. An
// empty file yields a nil header and a reader at EOF.
func downloadCSVReader(data []byte) (*csv.Reader, []string, error) {
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, utf8BOM)))
	r.FieldsPerRecord = -1
	r.ReuseRecord = true
	header, err := r.Read()
	if err == io.EOF {
		return r, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("parse downloaded csv: %w", err)
	}
	return r, append([]string(nil), header...), nil
}

func csvDownloadRows(r *csv.Reader, header []string) rowSource {
	return func() (record, bool, error) {
		line, err := r.Read()
		if err == io.EOF {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, fmt.Errorf("parse downloaded csv: %w", err)
		}
		rec := make(record, len(header))
		for i, col := range header {
			if i < len(line) {
				rec[col] = typedCell(col, line[i])
			} else {
				rec[col] = nil
			}
		}
		return rec, true, nil
	}
}

func parseJSONDownload(data []byte) ([]record, error) {
	var v any
	if err := decodeNumbers(data, &v); err != nil {
		return nil, fmt.Errorf("parse downloaded json: %w", err)
	}
	if obj, ok := v.(map[string]any); ok {
		v = obj["emails"]
	}
	list, ok := v.([]any)
	if !ok {
		return nil, errors.New("downloaded json has no list of results")
	}
	rows := make([]record, 0, len(list))
	for _, item := range list {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("downloaded json results are not objects")
		}
		rows = append(rows, record(obj))
	}
	return rows, nil
}

// typedCell restores the documented type of a CSV cell; blank means null.
func typedCell(col, s string) any {
	if s == "" {
		return nil
	}
	switch {
	case boolFields[col]:
		if b, err := strconv.ParseBool(s); err == nil {
			return b
		}
	case intFields[col]:
		if _, err := strconv.ParseInt(s, 10, 64); err == nil {
			return json.Number(s)
		}
	case floatFields[col]:
		if _, err := strconv.ParseFloat(s, 64); err == nil {
			return json.Number(s)
		}
	}
	return s
}

func hasKnownColumn(header []string) bool {
	for _, h := range header {
		for _, c := range CSVColumns {
			if h == c {
				return true
			}
		}
	}
	return false
}

// batchDocument is the batch payload as the API sent it, so a JSON save of a
// downloaded batch has the same shape as one with inline emails.
func batchDocument(b *api.BatchStatus) (map[string]any, error) {
	if b == nil {
		return map[string]any{}, nil
	}
	if raw := b.RawJSON(); len(raw) > 0 {
		var doc map[string]any
		if err := decodeNumbers(raw, &doc); err == nil && doc != nil {
			return doc, nil
		}
	}
	return typedRecord(b)
}

// writeUnknownDownload saves bytes we can't parse only when their own
// extension already matches what the user asked for.
func writeUnknownDownload(data []byte, name string, wantCSV bool, dest string) (int, error) {
	ext := strings.ToLower(path.Ext(name))
	if (wantCSV && ext == ".csv") || (!wantCSV && ext == ".json") {
		if err := atomicWrite(dest, data); err != nil {
			return 0, err
		}
		return 0, nil
	}
	want := "JSON"
	if wantCSV {
		want = "CSV"
	}
	return 0, fmt.Errorf("can't convert the downloaded results file (%s) to %s: unrecognized format", displayName(name), want)
}

func urlPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Path
}

func displayName(name string) string {
	if name == "" {
		return "no file name"
	}
	return path.Base(name)
}
