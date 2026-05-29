package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubHome points HOME at a temp dir and chdirs into another so the
// helpers operate on a hermetic filesystem.
func stubHome(t *testing.T) (home, cwd string) {
	t.Helper()
	home = t.TempDir()
	cwd = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Chdir(cwd)
	return
}

// TestContent_HasFrontmatter guards against an empty / malformed
// embed: every release must ship a SKILL.md with the YAML header
// Claude Code keys on.
func TestContent_HasFrontmatter(t *testing.T) {
	c := Content()
	if !strings.HasPrefix(c, "---\n") {
		t.Fatalf("SKILL.md must start with YAML frontmatter, got %q", c[:min(40, len(c))])
	}
	for _, want := range []string{"name: " + Name, "description:"} {
		if !strings.Contains(c, want) {
			t.Errorf("SKILL.md missing required frontmatter field: %s", want)
		}
	}
}

// TestTargets_IDsUniqueAndCanonicalFirst guards against typos in
// Targets()'s definition that would silently break --target.
func TestTargets_IDsUniqueAndCanonicalFirst(t *testing.T) {
	ts := Targets()
	if len(ts) == 0 || ts[0].ID != canonicalTargetID {
		t.Fatalf("canonical target must be first in Targets(); got %+v", ts)
	}
	seen := map[string]bool{}
	for _, tg := range ts {
		if seen[tg.ID] {
			t.Errorf("duplicate target ID: %s", tg.ID)
		}
		seen[tg.ID] = true
	}
}

// TestExpand covers tilde and absolute-path resolution.
func TestExpand(t *testing.T) {
	t.Setenv("HOME", "/tmp/fakehome")
	cases := []struct{ in, want string }{
		{"~", "/tmp/fakehome"},
		{"~/foo", "/tmp/fakehome/foo"},
		{"/abs/path", "/abs/path"},
	}
	for _, tc := range cases {
		got, err := Expand(tc.in)
		if err != nil {
			t.Fatalf("Expand(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("Expand(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestInstallCanonical writes to the expected canonical location and
// the file contains the embedded SKILL.md verbatim.
func TestInstallCanonical(t *testing.T) {
	home, _ := stubHome(t)
	path, err := InstallCanonical()
	if err != nil {
		t.Fatalf("InstallCanonical: %v", err)
	}
	want := filepath.Join(home, ".agents", "skills", "emailable", "SKILL.md")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != Content() {
		t.Error("installed content differs from embedded SKILL.md")
	}
}

// TestInstallDetected_LinksWhenDetected: with ~/.claude present and
// nothing else, the default install creates exactly one symlink and
// leaves the unsupported targets alone.
func TestInstallDetected_LinksWhenDetected(t *testing.T) {
	home, _ := stubHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := InstallDetected()
	if err != nil {
		t.Fatalf("InstallDetected: %v", err)
	}
	wantSkill := filepath.Join(home, ".agents", "skills", "emailable", "SKILL.md")
	if res.SkillPath != wantSkill {
		t.Errorf("SkillPath = %q, want %q", res.SkillPath, wantSkill)
	}
	if len(res.Links) != 1 {
		t.Fatalf("expected 1 link (claude-global), got %d: %+v", len(res.Links), res.Links)
	}
	link := res.Links[0]
	if link.Target.ID != "claude-global" {
		t.Errorf("linked target = %q, want claude-global", link.Target.ID)
	}
	wantLink := filepath.Join(home, ".claude", "skills", "emailable")
	if link.Path != wantLink {
		t.Errorf("link path = %q, want %q", link.Path, wantLink)
	}
	info, err := os.Lstat(wantLink)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected symlink at %s", wantLink)
	}
}

// TestInstallDetected_NoAgents: no agent dirs present → canonical
// still gets written, no symlinks created.
func TestInstallDetected_NoAgents(t *testing.T) {
	stubHome(t)
	res, err := InstallDetected()
	if err != nil {
		t.Fatalf("InstallDetected: %v", err)
	}
	if res.SkillPath == "" {
		t.Error("canonical SkillPath should always be set")
	}
	if len(res.Links) != 0 {
		t.Errorf("expected zero links when no agents detected, got %+v", res.Links)
	}
}

// TestInstallAll_LinksEveryGlobalTarget regardless of detection.
func TestInstallAll_LinksEveryGlobalTarget(t *testing.T) {
	stubHome(t)
	res, err := InstallAll()
	if err != nil {
		t.Fatalf("InstallAll: %v", err)
	}
	gotIDs := map[string]bool{}
	for _, l := range res.Links {
		gotIDs[l.Target.ID] = true
	}
	for _, want := range []string{"claude-global", "opencode-global", "codex-global"} {
		if !gotIDs[want] {
			t.Errorf("InstallAll should have linked %s, got %v", want, gotIDs)
		}
	}
	if gotIDs[canonicalTargetID] {
		t.Errorf("InstallAll should not link canonical to itself, got %v", gotIDs)
	}
}

// TestInstallOne installs a single target only.
func TestInstallOne(t *testing.T) {
	home, _ := stubHome(t)
	loc, ok := LookupTarget("opencode-global")
	if !ok {
		t.Fatal("LookupTarget(opencode-global) miss")
	}
	res, err := InstallOne(loc)
	if err != nil {
		t.Fatalf("InstallOne: %v", err)
	}
	if len(res.Links) != 1 || res.Links[0].Target.ID != "opencode-global" {
		t.Errorf("expected single opencode-global link, got %+v", res.Links)
	}
	want := filepath.Join(home, ".config", "opencode", "skill", "emailable")
	if res.Links[0].Path != want {
		t.Errorf("link path = %q, want %q", res.Links[0].Path, want)
	}
}

// TestInstallOne_Canonical: installing the canonical target alone
// only writes the file — no extra symlink rows in the result.
func TestInstallOne_Canonical(t *testing.T) {
	stubHome(t)
	res, err := InstallOne(Canonical())
	if err != nil {
		t.Fatalf("InstallOne(Canonical): %v", err)
	}
	if len(res.Links) != 0 {
		t.Errorf("canonical install should record zero links, got %+v", res.Links)
	}
}

// TestReinstallOverwrites: installing twice in a row replaces an
// existing symlink target instead of erroring.
func TestReinstallOverwrites(t *testing.T) {
	home, _ := stubHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallDetected(); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if _, err := InstallDetected(); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
}

// TestNormalizeCustomPath covers the three shapes the wizard accepts:
// a literal .md file, a path that already ends in "emailable", and a
// bare parent directory.
func TestNormalizeCustomPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"~/foo/SKILL.md", "~/foo/SKILL.md"},
		{"~/foo/custom.md", "~/foo/custom.md"},
		{"~/foo/emailable", "~/foo/emailable/SKILL.md"},
		{"~/foo/emailable/", "~/foo/emailable/SKILL.md"},
		{"emailable", "emailable/SKILL.md"},
		{"~/foo", "~/foo/emailable/SKILL.md"},
		{"  ~/with-spaces  ", "~/with-spaces/emailable/SKILL.md"},
	}
	for _, tc := range cases {
		if got := NormalizeCustomPath(tc.in); got != tc.want {
			t.Errorf("NormalizeCustomPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestInstallToFile writes SKILL.md to a literal path (including the
// .md filename) and creates missing parent dirs.
func TestInstallToFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "a", "b", "MyCustom.md")
	got, err := InstallToFile(target)
	if err != nil {
		t.Fatalf("InstallToFile: %v", err)
	}
	if got != target {
		t.Errorf("returned path = %q, want %q", got, target)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != Content() {
		t.Error("installed content differs from embedded SKILL.md")
	}
}

// TestCodexGlobal_HonorsEnv: $CODEX_HOME overrides the default path.
func TestCodexGlobal_HonorsEnv(t *testing.T) {
	stubHome(t)
	t.Setenv("CODEX_HOME", "/opt/codex")
	loc, ok := LookupTarget("codex-global")
	if !ok {
		t.Fatal("LookupTarget(codex-global) miss")
	}
	if want := "/opt/codex/skills/emailable"; loc.Dir != want {
		t.Errorf("codex-global.Dir = %q, want %q", loc.Dir, want)
	}
}
