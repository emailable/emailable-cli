package output

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/emailable/emailable-cli/internal/api"
)

// SaveOptions controls how WriteResults serializes data to disk.
type SaveOptions struct {
	Path      string
	ForceJSON bool
	Stderr    *os.File // nil means os.Stderr
}

// CSVColumns is the fixed CSV column order: every documented field of the
// verify response, identity first, then name parts, then flags and server
// details. Keys outside this list are appended after it, sorted by name.
var CSVColumns = []string{
	"email", "state", "reason", "score", "domain", "user",
	"first_name", "last_name", "full_name", "gender", "birth_year",
	"free", "role", "disposable", "accept_all", "mailbox_full", "no_reply",
	"did_you_mean", "mx_record", "smtp_provider", "tag", "duration",
}

// record is one verify result decoded from the API's JSON, so a key the API
// omitted (e.g. via response_fields) or sent as null stays distinguishable
// from a zero value.
type record map[string]any

// WriteResults writes v to opts.Path atomically and returns the row count.
// Unknown extensions fall back to JSON with a stderr note.
func WriteResults(v any, opts SaveOptions) (int, error) {
	if opts.Path == "" {
		return 0, fmt.Errorf("output path is required")
	}
	if useCSV(opts) {
		rows, ok, err := recordsFor(v)
		if err != nil {
			return 0, err
		}
		if !ok {
			fmt.Fprintln(stderrFor(opts), "note: data shape not supported for CSV; writing JSON")
			return writeJSON(v, opts.Path)
		}
		return writeCSV(rows, opts.Path)
	}
	return writeJSON(v, opts.Path)
}

func stderrFor(opts SaveOptions) *os.File {
	if opts.Stderr != nil {
		return opts.Stderr
	}
	return os.Stderr
}

func useCSV(opts SaveOptions) bool {
	if opts.ForceJSON {
		return false
	}
	switch strings.ToLower(filepath.Ext(opts.Path)) {
	case ".csv":
		return true
	case ".json":
		return false
	default:
		fmt.Fprintln(stderrFor(opts), "note: unrecognized extension; writing JSON")
		return false
	}
}

// recordsFor builds CSV rows from the raw API body when available, falling
// back to re-encoding the typed struct for values built in code.
func recordsFor(v any) ([]record, bool, error) {
	switch t := v.(type) {
	case *api.VerifyResult:
		if t == nil {
			return nil, true, nil
		}
		r, err := verifyRecord(t)
		if err != nil {
			return nil, true, err
		}
		return []record{r}, true, nil
	case api.VerifyResult:
		return recordsFor(&t)
	case *api.BatchStatus:
		if t == nil {
			return nil, true, nil
		}
		rows, err := batchRecords(t)
		return rows, true, err
	case api.BatchStatus:
		return recordsFor(&t)
	case []api.VerifyResult:
		rows := make([]record, 0, len(t))
		for i := range t {
			r, err := verifyRecord(&t[i])
			if err != nil {
				return nil, true, err
			}
			rows = append(rows, r)
		}
		return rows, true, nil
	default:
		return nil, false, nil
	}
}

func verifyRecord(r *api.VerifyResult) (record, error) {
	if raw := r.RawJSON(); len(raw) > 0 {
		var rec record
		if err := decodeNumbers(raw, &rec); err == nil && rec != nil {
			return rec, nil
		}
	}
	return typedRecord(r)
}

func batchRecords(b *api.BatchStatus) ([]record, error) {
	if raw := b.RawJSON(); len(raw) > 0 {
		var doc struct {
			Emails []record `json:"emails"`
		}
		if err := decodeNumbers(raw, &doc); err == nil {
			return doc.Emails, nil
		}
	}
	rows := make([]record, 0, len(b.Emails))
	for i := range b.Emails {
		r, err := verifyRecord(&b.Emails[i])
		if err != nil {
			return nil, err
		}
		rows = append(rows, r)
	}
	return rows, nil
}

func typedRecord(v any) (record, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}
	var rec record
	if err := decodeNumbers(b, &rec); err != nil {
		return nil, fmt.Errorf("decode result: %w", err)
	}
	return rec, nil
}

// decodeNumbers keeps numbers as json.Number so scores and durations render
// exactly as the API sent them.
func decodeNumbers(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return dec.Decode(v)
}

func resultCount(v any) int {
	switch t := v.(type) {
	case *api.VerifyResult:
		if t == nil {
			return 0
		}
		return 1
	case api.VerifyResult:
		return 1
	case *api.BatchStatus:
		if t == nil {
			return 0
		}
		return len(t.Emails)
	case api.BatchStatus:
		return len(t.Emails)
	case []api.VerifyResult:
		return len(t)
	default:
		return 0
	}
}

func writeJSON(v any, path string) (int, error) {
	data, err := marshalDocument(v, false)
	if err != nil {
		return 0, fmt.Errorf("marshal json: %w", err)
	}
	if err := atomicWrite(path, data); err != nil {
		return 0, err
	}
	return resultCount(v), nil
}

// csvHeaderFor returns CSVColumns followed by any other keys present in
// rows, sorted by name.
func csvHeaderFor(rows []record) []string {
	extra := map[string]bool{}
	for _, r := range rows {
		for k := range r {
			extra[k] = true
		}
	}
	return csvHeaderWith(extra)
}

// csvHeaderWith returns CSVColumns followed by the keys of present that
// aren't already among them, sorted by name.
func csvHeaderWith(present map[string]bool) []string {
	known := make(map[string]bool, len(CSVColumns))
	for _, c := range CSVColumns {
		known[c] = true
	}
	names := make([]string, 0, len(present))
	for k := range present {
		if !known[k] {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	header := append([]string(nil), CSVColumns...)
	return append(header, names...)
}

// csvCell renders one value; absent and null are both blank.
func csvCell(v any, present bool) string {
	if !present || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case json.Number:
		return t.String()
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprint(t)
		}
		return string(b)
	}
}

// rowSource yields records one at a time, so a large download converts row
// by row instead of being held in memory whole. ok=false ends the stream.
type rowSource func() (rec record, ok bool, err error)

func sliceRows(rows []record) rowSource {
	i := 0
	return func() (record, bool, error) {
		if i >= len(rows) {
			return nil, false, nil
		}
		i++
		return rows[i-1], true, nil
	}
}

func encodeCSV(w io.Writer, header []string, next rowSource) (int, error) {
	cw := csv.NewWriter(w)
	if err := cw.Write(header); err != nil {
		return 0, fmt.Errorf("write csv header: %w", err)
	}
	n := 0
	line := make([]string, len(header))
	for {
		r, ok, err := next()
		if err != nil {
			return n, err
		}
		if !ok {
			break
		}
		for i, col := range header {
			v, present := r[col]
			line[i] = csvCell(v, present)
		}
		if err := cw.Write(line); err != nil {
			return n, fmt.Errorf("write csv row: %w", err)
		}
		n++
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return n, fmt.Errorf("flush csv: %w", err)
	}
	return n, nil
}

func writeCSV(rows []record, path string) (int, error) {
	return writeCSVRows(path, csvHeaderFor(rows), sliceRows(rows))
}

func writeCSVRows(path string, header []string, next rowSource) (int, error) {
	var n int
	err := atomicWriteWith(path, func(w io.Writer) error {
		var err error
		n, err = encodeCSV(w, header, next)
		return err
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

func atomicWrite(path string, data []byte) error {
	return atomicWriteWith(path, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// atomicWriteWith streams fill's output to a temp file beside path, then
// renames it into place so a failed write never leaves a partial file.
func atomicWriteWith(path string, fill func(io.Writer) error) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmp)
		}
	}()
	bw := bufio.NewWriter(f)
	if err := fill(bw); err != nil {
		_ = f.Close()
		return err
	}
	if err := bw.Flush(); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	cleanup = false
	return nil
}
