package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/emailable/emailable-cli/internal/api"
)

// SaveOptions controls how WriteResults serializes data to disk.
type SaveOptions struct {
	// Path is the destination file path; required.
	Path string
	// ForceJSON forces JSON output regardless of file extension.
	ForceJSON bool
	// Stderr receives non-fatal notes (e.g. unrecognized extension warning).
	// If nil, os.Stderr is used.
	Stderr *os.File
}

// csvHeader is the canonical CSV column order used when flattening
// VerifyResult records. Floats (like Duration) are intentionally skipped —
// they're not useful in a spreadsheet.
var csvHeader = []string{
	"email", "state", "score", "reason", "domain",
	"disposable", "accept_all", "role", "free",
	"mx_record", "smtp_provider", "did_you_mean",
	"first_name", "last_name", "gender",
}

// WriteResults writes v to opts.Path atomically (via .tmp + rename) and
// returns the number of result rows written.
//
// Supported shapes:
//   - *api.VerifyResult       — single result (JSON object, or 1-row CSV)
//   - *api.BatchStatus        — batch with embedded emails
//   - []api.VerifyResult      — explicit slice of results
//   - *api.Account            — account info (JSON only; CSV falls back to JSON
//     with a stderr note)
//
// Format selection:
//   - opts.ForceJSON => JSON
//   - extension .json => JSON
//   - extension .csv  => CSV when v is flattenable; JSON otherwise (with note)
//   - any other extension => JSON (with stderr note about unrecognized ext)
//
// Files are written with mode 0644.
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
		// Unknown extension: warn and write JSON.
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

// flattenForCSV returns the VerifyResult rows extractable from v, and a
// bool indicating whether v is a shape we can render as CSV at all.
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

// resultCount mirrors flattenForCSV's row count for the JSON path so the
// caller can print "Saved N results" consistently regardless of format.
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
		// Unknown shapes (e.g. *api.Account): we don't have a row count to
		// report. Return 0 and let callers reword their success message
		// accordingly ("Saved to <file>" instead of "Saved N results...").
		return 0
	}
}

func writeJSON(v any, path string) (int, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("marshal json: %w", err)
	}
	// MarshalIndent doesn't append a trailing newline; add one for POSIX
	// friendliness.
	data = append(data, '\n')
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
	// Best-effort cleanup of the tmp file if anything below fails before
	// rename. After a successful rename the tmp path no longer exists, so
	// Remove there is a no-op.
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

// atomicWrite writes data to path via path+".tmp" and renames into place.
// The tmp file is removed if rename fails. Mode is 0644.
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
