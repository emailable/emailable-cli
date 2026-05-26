package cmd

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// emailShape is a deliberately loose "anything@anything.anything" check.
// It's only used to distinguish literal-email args from misspelled file
// paths / garbage at the CLI boundary — full deliverability validation is
// the API's job.
var emailShape = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// looksLikeEmail reports whether s plausibly resembles an email address.
func looksLikeEmail(s string) bool {
	return emailShape.MatchString(strings.TrimSpace(s))
}

// stdinSource returns the reader stdin lines should be read from when `-`
// appears in the arg list, and whether stdin has data piped to it (i.e. is
// not a TTY). Overridable in tests.
var stdinSource = func() (io.Reader, bool) {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return os.Stdin, false
	}
	// A pipe or redirected file has ModeCharDevice unset. A TTY does not.
	piped := (fi.Mode() & os.ModeCharDevice) == 0
	return os.Stdin, piped
}

// collectEmails flattens a list of CLI inputs (literal emails or paths to
// .csv/.json/.txt files) into a deduped slice in first-seen order. Each
// non-path argument is treated as a single email — comma-separated lists
// are intentionally NOT split (commas in quoted local parts are technically
// valid per RFC 5321, and shell-natural CLI input is space-separated). For
// pasted comma-separated lists, save them to a .csv file and pass that.
//
// The special argument `-` reads newline-delimited emails from stdin
// (treated as plain-text format, like a .txt file). It may appear at most
// once and requires stdin to be piped — passing `-` from an interactive
// TTY is an error.
//
// Returns an error if no emails remain.
func collectEmails(inputs []string, field string) ([]string, error) {
	var out []string
	seen := make(map[string]struct{})

	add := func(email string) {
		email = strings.TrimSpace(email)
		if email == "" {
			return
		}
		if _, ok := seen[email]; ok {
			return
		}
		seen[email] = struct{}{}
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
		// Not an existing file — must be a literal email. Reject anything
		// that doesn't match the basic email shape so typos like a missing
		// extension or a misspelled path don't get silently submitted to
		// the API as an "email".
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

// isPath returns true if the input looks like a path to an existing file:
// has a recognised extension or contains a path separator, and the file
// exists on disk.
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

// looksLikeBatchInput returns true if the argument passed to `verify` looks
// like a path that was intended for `batch verify`. Used to surface a
// migration hint after the split of the overloaded `verify` command.
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

func readCSV(path, field string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, NewInvalidInputf("open %s: %v", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	records, err := r.ReadAll()
	if err != nil {
		return nil, NewInvalidInputf("parse csv %s: %v", path, err)
	}
	if len(records) == 0 {
		return nil, nil
	}
	header := records[0]
	colIdx := -1

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
	for _, row := range records[1:] {
		if colIdx < len(row) {
			out = append(out, row[colIdx])
		}
	}
	return out, nil
}

func readJSON(path, field string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, NewInvalidInputf("open %s: %v", path, err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, NewInvalidInputf("read %s: %v", path, err)
	}

	var top any
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, NewInvalidInputf("parse json %s: %v", path, err)
	}

	return extractJSONEmails(top, field, path)
}

func extractJSONEmails(top any, field, path string) ([]string, error) {
	switch v := top.(type) {
	case []any:
		if len(v) == 0 {
			return nil, nil
		}
		// Array of strings?
		if _, ok := v[0].(string); ok {
			out := make([]string, 0, len(v))
			for _, item := range v {
				if s, ok := item.(string); ok {
					out = append(out, s)
				}
			}
			return out, nil
		}
		// Array of objects → need field
		if _, ok := v[0].(map[string]any); ok {
			if field == "" {
				return nil, NewInvalidInputf("array of objects in %s; specify --field <name>", path)
			}
			out := make([]string, 0, len(v))
			for _, item := range v {
				obj, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if s, ok := obj[field].(string); ok {
					out = append(out, s)
				}
			}
			return out, nil
		}
		return nil, NewInvalidInputf("unsupported json array element type in %s", path)
	case map[string]any:
		// Top-level object: find array-valued field(s).
		if field != "" {
			arr, ok := v[field].([]any)
			if !ok {
				return nil, NewInvalidInputf("field %q not found or not an array in %s", field, path)
			}
			return extractJSONEmails(arr, field, path)
		}
		var arrKey string
		count := 0
		for k, val := range v {
			if _, ok := val.([]any); ok {
				arrKey = k
				count++
			}
		}
		if count == 0 {
			return nil, NewInvalidInputf("no array field found in %s", path)
		}
		if count > 1 {
			return nil, NewInvalidInputf("multiple array fields in %s; specify --field <name>", path)
		}
		return extractJSONEmails(v[arrKey], field, path)
	default:
		return nil, NewInvalidInputf("unsupported json top-level type in %s", path)
	}
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

// readTXTReader reads newline-delimited emails from r using the same
// permissive rules as readTXT: lines are split on commas too, and the
// downstream collectEmails add() trims whitespace and skips empties. Used
// for both .txt files and stdin (`-`).
func readTXTReader(r io.Reader) ([]string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		for _, part := range strings.Split(line, ",") {
			out = append(out, part)
		}
	}
	return out, nil
}
