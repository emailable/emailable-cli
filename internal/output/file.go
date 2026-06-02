package output

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
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

// Duration and other floats are omitted — not useful in a spreadsheet.
var csvHeader = []string{
	"email", "state", "score", "reason", "domain",
	"disposable", "accept_all", "role", "free",
	"mx_record", "smtp_provider", "did_you_mean",
	"first_name", "last_name", "gender",
}

// WriteResults writes v to opts.Path atomically and returns the row count.
// Unknown extensions fall back to JSON with a stderr note.
func WriteResults(v any, opts SaveOptions) (int, error) {
	if opts.Path == "" {
		return 0, fmt.Errorf("output path is required")
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	ext := strings.ToLower(filepath.Ext(opts.Path))

	useCSV := false
	switch {
	case opts.ForceJSON:
		useCSV = false
	case ext == ".csv":
		useCSV = true
	case ext == ".json":
		useCSV = false
	default:
		fmt.Fprintln(stderr, "note: unrecognized extension; writing JSON")
		useCSV = false
	}

	if useCSV {
		rows, ok := flattenForCSV(v)
		if !ok {
			fmt.Fprintln(stderr, "note: data shape not supported for CSV; writing JSON")
			return writeJSON(v, opts.Path)
		}
		return writeCSV(rows, opts.Path)
	}
	return writeJSON(v, opts.Path)
}

func flattenForCSV(v any) ([]api.VerifyResult, bool) {
	switch t := v.(type) {
	case *api.VerifyResult:
		if t == nil {
			return nil, true
		}
		return []api.VerifyResult{*t}, true
	case api.VerifyResult:
		return []api.VerifyResult{t}, true
	case *api.BatchStatus:
		if t == nil {
			return nil, true
		}
		return t.Emails, true
	case api.BatchStatus:
		return t.Emails, true
	case []api.VerifyResult:
		return t, true
	default:
		return nil, false
	}
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

func writeCSV(rows []api.VerifyResult, path string) (int, error) {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, fmt.Errorf("create %s: %w", tmp, err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmp)
		}
	}()

	w := csv.NewWriter(f)
	if err := w.Write(csvHeader); err != nil {
		f.Close()
		return 0, fmt.Errorf("write csv header: %w", err)
	}
	for _, r := range rows {
		rec := []string{
			r.Email,
			r.State,
			strconv.Itoa(r.Score),
			r.Reason,
			r.Domain,
			strconv.FormatBool(r.Disposable),
			strconv.FormatBool(r.AcceptAll),
			strconv.FormatBool(r.Role),
			strconv.FormatBool(r.Free),
			r.MXRecord,
			r.SMTPProvider,
			r.DidYouMean,
			r.FirstName,
			r.LastName,
			r.Gender,
		}
		if err := w.Write(rec); err != nil {
			f.Close()
			return 0, fmt.Errorf("write csv row: %w", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		f.Close()
		return 0, fmt.Errorf("flush csv: %w", err)
	}
	if err := f.Close(); err != nil {
		return 0, fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return 0, fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	cleanup = false
	return len(rows), nil
}

func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmp)
		}
	}()
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	cleanup = false
	return nil
}
