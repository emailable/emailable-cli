package cmd

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// emailShape is a loose check used only to distinguish literal-email args from
// misspelled paths at the CLI boundary — real validation is the API's job.
var emailShape = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

func looksLikeEmail(s string) bool {
	return emailShape.MatchString(strings.TrimSpace(s))
}

// stdinSource is overridable in tests.
var stdinSource = func() (io.Reader, bool) {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return os.Stdin, false
	}
	piped := (fi.Mode() & os.ModeCharDevice) == 0
	return os.Stdin, piped
}

// collectEmails flattens CLI inputs (literal emails or .csv/.json/.txt paths)
// into a deduped slice. Comma-separated args are not split — shell input is
// space-separated, and commas in quoted local-parts are RFC 5321 valid.
// `-` reads newline-delimited emails from a piped stdin (once only).
func collectEmails(inputs []string, field string) ([]string, error) {
	var out []string
	seen := make(map[string]struct{})

	add := func(email string) {
		email = strings.TrimSpace(email)
		if email == "" {
			return
		}
		// Dedupe case-insensitively to match the server, which lowercases
		// addresses. The first spelling seen is the one submitted.
		key := strings.ToLower(email)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, email)
	}

	stdinUsed := false
	for _, in := range inputs {
		if in == "-" {
			if stdinUsed {
				return nil, NewInvalidInput("`-` can only be used once: stdin can only be read once")
			}
			stdinUsed = true
			r, piped := stdinSource()
			if !piped {
				return nil, NewInvalidInput("cannot read from stdin: no input piped")
			}
			items, err := readTXTReader(r)
			if err != nil {
				return nil, err
			}
			for _, e := range items {
				add(e)
			}
			continue
		}
		if isPath(in) {
			ext := strings.ToLower(filepath.Ext(in))
			switch ext {
			case ".csv":
				items, err := readCSV(in, field)
				if err != nil {
					return nil, err
				}
				for _, e := range items {
					add(e)
				}
			case ".json":
				items, err := readJSON(in, field)
				if err != nil {
					return nil, err
				}
				for _, e := range items {
					add(e)
				}
			default: // .txt or other
				items, err := readTXT(in)
				if err != nil {
					return nil, err
				}
				for _, e := range items {
					add(e)
				}
			}
			continue
		}
		if !looksLikeEmail(in) {
			if looksLikeBatchInput(in) {
				return nil, NewInvalidInputf("file not found: %s", in)
			}
			return nil, NewInvalidInputf("%q is not a valid email or existing file", in)
		}
		add(in)
	}

	if len(out) == 0 {
		return nil, NewInvalidInput("no emails to verify")
	}
	return out, nil
}

func isPath(s string) bool {
	lower := strings.ToLower(s)
	hasExt := strings.HasSuffix(lower, ".csv") ||
		strings.HasSuffix(lower, ".json") ||
		strings.HasSuffix(lower, ".txt")
	hasSep := strings.ContainsAny(s, `/\`)
	if !hasExt && !hasSep {
		return false
	}
	info, err := os.Stat(s)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func looksLikeBatchInput(s string) bool {
	lower := strings.ToLower(s)
	if strings.HasSuffix(lower, ".csv") ||
		strings.HasSuffix(lower, ".json") ||
		strings.HasSuffix(lower, ".txt") {
		return true
	}
	if strings.ContainsAny(s, `/\`) {
		return true
	}
	return false
}

// stripBOM drops a leading UTF-8 byte order mark. Excel writes one on CSV
// export, and left in place it breaks header matching and JSON parsing.
func stripBOM(data []byte) []byte {
	return bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
}

func readCSV(path, field string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, NewInvalidInputf("open %s: %v", path, err)
	}

	r := csv.NewReader(bytes.NewReader(stripBOM(data)))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil, NewInvalidInputf("parse csv %s: %v", path, err)
	}
	if len(records) == 0 {
		return nil, nil
	}
	header := records[0]
	colIdx := -1
	dataStart := 1

	if field != "" {
		for i, h := range header {
			if strings.EqualFold(strings.TrimSpace(h), field) {
				colIdx = i
				break
			}
		}
		if colIdx == -1 {
			return nil, NewInvalidInputf("field %q not found in %s", field, path)
		}
	} else if len(header) == 1 {
		colIdx = 0
		// A one-column file that starts with an address has no header row.
		if looksLikeEmail(header[0]) {
			dataStart = 0
		}
	} else {
		for i, h := range header {
			if strings.EqualFold(strings.TrimSpace(h), "email") {
				colIdx = i
				break
			}
		}
		if colIdx == -1 {
			return nil, NewInvalidInputf("multiple columns found in %s; specify --field <name>", path)
		}
	}

	var out []string
	for _, row := range records[dataStart:] {
		if colIdx < len(row) {
			out = append(out, row[colIdx])
		}
	}
	return out, nil
}

func readJSON(path, field string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, NewInvalidInputf("open %s: %v", path, err)
	}

	var top any
	if err := json.Unmarshal(stripBOM(data), &top); err != nil {
		return nil, NewInvalidInputf("parse json %s: %v", path, err)
	}

	if field == "" {
		return detectJSONEmails(top, path)
	}
	return extractJSONPath(top, field, path)
}

// extractJSONPath reads the strings at a dotted path such as `contacts.email`.
// An array anywhere along the way applies the rest of the path to each of its
// elements, so `email` against `[{"email":...}]` reads every object.
func extractJSONPath(top any, field, path string) ([]string, error) {
	segs := strings.Split(field, ".")
	// `{"contacts":[...]}` with `--field email`: when the first segment isn't
	// a top-level key, descend into the object's only array.
	if obj, ok := top.(map[string]any); ok {
		_, literal := lookupKey(obj, field)
		if _, ok := lookupKey(obj, segs[0]); !ok && !literal {
			if arr, ok := soleArray(obj); ok {
				top = arr
			}
		}
	}

	var out []string
	found := false
	var walk func(v any, segs []string)
	walk = func(v any, segs []string) {
		if arr, ok := v.([]any); ok {
			for _, item := range arr {
				walk(item, segs)
			}
			return
		}
		if len(segs) == 0 {
			found = true
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
			return
		}
		obj, ok := v.(map[string]any)
		if !ok {
			return
		}
		// A key that literally contains dots (`"contact.email"`) wins over
		// splitting it into a nested path.
		if len(segs) > 1 {
			if next, ok := lookupKey(obj, strings.Join(segs, ".")); ok {
				walk(next, nil)
				return
			}
		}
		if next, ok := lookupKey(obj, segs[0]); ok {
			walk(next, segs[1:])
		}
	}
	walk(top, segs)

	// A plain array of strings has no keys to walk; `--field` doesn't apply,
	// so read it as-is like the auto-detect path does.
	if arr, ok := top.([]any); ok && !found {
		for _, item := range arr {
			if s, ok := item.(string); ok {
				found = true
				out = append(out, s)
			}
		}
	}

	if !found {
		return nil, NewInvalidInputf("field %q not found in %s", field, path)
	}
	return out, nil
}

// detectJSONEmails handles input without `--field`: an array of strings, an
// array of objects with an `email` key, or an object wrapping one such array.
func detectJSONEmails(top any, path string) ([]string, error) {
	const hint = "specify --field <path> (e.g. --field contacts.email)"
	switch v := top.(type) {
	case []any:
		var out []string
		sawObject, sawEmailKey := false, false
		for _, item := range v {
			switch e := item.(type) {
			case string:
				out = append(out, e)
			case map[string]any:
				sawObject = true
				if val, ok := lookupKey(e, "email"); ok {
					sawEmailKey = true
					if s, ok := val.(string); ok {
						out = append(out, s)
					}
				}
			}
		}
		if sawObject && !sawEmailKey {
			return nil, NewInvalidInputf("no email key found in %s; %s", path, hint)
		}
		if len(v) > 0 && !sawObject && len(out) == 0 {
			return nil, NewInvalidInputf("unsupported json array element type in %s", path)
		}
		return out, nil
	case map[string]any:
		arr, ok := soleArray(v)
		if !ok {
			return nil, NewInvalidInputf("expected exactly one array field in %s; %s", path, hint)
		}
		return detectJSONEmails(arr, path)
	default:
		return nil, NewInvalidInputf("unsupported json top-level type in %s", path)
	}
}

// lookupKey prefers an exact key match and falls back to a case-insensitive
// one, so `email` also finds `Email`. When several keys match ignoring case,
// the first in sorted order wins so the result doesn't depend on map order.
func lookupKey(obj map[string]any, key string) (any, bool) {
	if v, ok := obj[key]; ok {
		return v, true
	}
	best, found := "", false
	for k := range obj {
		if strings.EqualFold(k, key) && (!found || k < best) {
			best, found = k, true
		}
	}
	if !found {
		return nil, false
	}
	return obj[best], true
}

// soleArray returns the value of obj's only array-valued key, if it has
// exactly one.
func soleArray(obj map[string]any) ([]any, bool) {
	var found []any
	count := 0
	for _, val := range obj {
		if arr, ok := val.([]any); ok {
			found = arr
			count++
		}
	}
	return found, count == 1
}

func readTXT(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, NewInvalidInputf("open %s: %v", path, err)
	}
	defer f.Close()
	out, err := readTXTReader(f)
	if err != nil {
		return nil, NewInvalidInputf("read %s: %v", path, err)
	}
	return out, nil
}

func readTXTReader(r io.Reader) ([]string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(stripBOM(data)), "\n") {
		out = append(out, strings.Split(line, ",")...)
	}
	return out, nil
}
