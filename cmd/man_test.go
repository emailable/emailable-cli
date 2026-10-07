package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
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

// TestMan_GoreleaserCaskManpages asserts the hand-maintained
// `homebrew_casks[].manpages` list in .goreleaser.yaml matches the pages
// `emailable man` generates. Casks don't accept globs, so a command added
// without updating that list would ship without its page on Homebrew.
func TestMan_GoreleaserCaskManpages(t *testing.T) {
	dir := t.TempDir()
	res := runRoot(t, "man", "--output", dir)
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	generated := make(map[string]bool, len(entries))
	for _, e := range entries {
		generated["man/"+e.Name()] = true
	}

	data, err := os.ReadFile("../.goreleaser.yaml")
	if err != nil {
		t.Fatalf("read .goreleaser.yaml: %v", err)
	}
	var cfg struct {
		HomebrewCasks []struct {
			Name     string   `yaml:"name"`
			Manpages []string `yaml:"manpages"`
		} `yaml:"homebrew_casks"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse .goreleaser.yaml: %v", err)
	}
	if len(cfg.HomebrewCasks) == 0 {
		t.Fatal("expected at least one homebrew_casks entry in .goreleaser.yaml")
	}

	for _, cask := range cfg.HomebrewCasks {
		listed := make(map[string]bool, len(cask.Manpages))
		for _, p := range cask.Manpages {
			listed[p] = true
		}
		var missing, extra []string
		for p := range generated {
			if !listed[p] {
				missing = append(missing, p)
			}
		}
		for p := range listed {
			if !generated[p] {
				extra = append(extra, p)
			}
		}
		if len(missing) == 0 && len(extra) == 0 {
			continue
		}
		slices.Sort(missing)
		slices.Sort(extra)
		var b strings.Builder
		b.WriteString("homebrew cask " + cask.Name + " manpages in .goreleaser.yaml are out of sync with `emailable man`:\n")
		for _, p := range missing {
			b.WriteString("  + " + p + " (generated, not listed)\n")
		}
		for _, p := range extra {
			b.WriteString("  - " + p + " (listed, not generated)\n")
		}
		t.Error(b.String())
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
