package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMan_GeneratesPages runs `emailable man --output DIR` and asserts the
// expected per-subcommand pages exist on disk.
func TestMan_GeneratesPages(t *testing.T) {
	dir := t.TempDir()
	res := runRoot(t, "man", "--output", dir)
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected man pages to be generated, dir is empty")
	}

	// At a minimum the root page must exist.
	if _, err := os.Stat(filepath.Join(dir, "emailable.1")); err != nil {
		t.Errorf("expected emailable.1, stat err: %v", err)
	}

	// Spot-check a subcommand page (verify) — cobra/doc generates one per
	// subcommand with the parent name in front.
	wantPrefixes := []string{"emailable-verify", "emailable-batch", "emailable-login"}
	have := make(map[string]bool)
	for _, e := range entries {
		have[e.Name()] = true
	}
	for _, p := range wantPrefixes {
		found := false
		for name := range have {
			if strings.HasPrefix(name, p) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected a man page starting with %q, have %v", p, have)
		}
	}
}

// TestMan_RequiresOutput surfaces the local validation error when --output
// is omitted.
func TestMan_RequiresOutput(t *testing.T) {
	res := runRoot(t, "man")
	if res.Err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(res.Err.Error(), "--output DIR is required") {
		t.Errorf("expected required-flag error, got %v", res.Err)
	}
	if got := errorCode(res.Err); got != codeInvalidInput {
		t.Errorf("expected invalid_input code, got %q", got)
	}
}
