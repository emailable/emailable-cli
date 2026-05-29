package cmd

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubAgentEnv points HOME and CWD at fresh temp dirs and resets
// CODEX_HOME so the multi-target detection logic operates on a clean
// filesystem.
func stubAgentEnv(t *testing.T) (home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	return home
}

// TestSkillInstall_DefaultCanonical: with no detected agents the
// default install still writes the canonical file and reports no
// links.
func TestSkillInstall_DefaultCanonical(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())
	home := stubAgentEnv(t)

	res := runRoot(t, "skill", "install")
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	want := filepath.Join(home, ".agents", "skills", "emailable", "SKILL.md")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("canonical SKILL.md not at %s: %v", want, err)
	}
	if !strings.Contains(res.Stdout.String(), want) {
		t.Errorf("stdout should mention canonical path, got %q", res.Stdout.String())
	}
}

// TestSkillInstall_LinksDetectedClaude: with ~/.claude present the
// default install symlinks into it.
func TestSkillInstall_LinksDetectedClaude(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())
	home := stubAgentEnv(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	res := runRoot(t, "skill", "install")
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	link := filepath.Join(home, ".claude", "skills", "emailable")
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat link: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected symlink at %s", link)
	}
	if !strings.Contains(res.Stdout.String(), "Claude Code (Global)") {
		t.Errorf("stdout should mention linked Claude target, got %q", res.Stdout.String())
	}
}

// TestSkillInstall_Target_OpenCodeGlobal: --target picks a single
// non-default location.
func TestSkillInstall_Target_OpenCodeGlobal(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())
	home := stubAgentEnv(t)

	res := runRoot(t, "skill", "install", "--target", "opencode-global")
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	link := filepath.Join(home, ".config", "opencode", "skill", "emailable")
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected symlink at %s (err=%v)", link, err)
	}
}

// TestSkillInstall_All: --all symlinks every known global target,
// even ones whose detect dir is missing.
func TestSkillInstall_All(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())
	home := stubAgentEnv(t)

	res := runRoot(t, "skill", "install", "--all")
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	for _, link := range []string{
		filepath.Join(home, ".claude", "skills", "emailable"),
		filepath.Join(home, ".config", "opencode", "skill", "emailable"),
		filepath.Join(home, ".codex", "skills", "emailable"),
	} {
		if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("expected symlink at %s (err=%v)", link, err)
		}
	}
}

// TestSkillInstall_DirSkipsLinks: --dir writes only to the requested
// dir and creates no symlinks.
func TestSkillInstall_DirSkipsLinks(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())
	home := stubAgentEnv(t)
	custom := filepath.Join(t.TempDir(), "nested")

	res := runRoot(t, "skill", "install", "--dir", custom)
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	if _, err := os.Stat(filepath.Join(custom, "SKILL.md")); err != nil {
		t.Fatalf("custom SKILL.md missing: %v", err)
	}
	// No canonical write should have happened.
	if _, err := os.Stat(filepath.Join(home, ".agents")); !os.IsNotExist(err) {
		t.Errorf("--dir should not touch the canonical location; err=%v", err)
	}
}

// TestSkillInstall_RejectsConflictingFlags: --target + --all together
// is a user error.
func TestSkillInstall_RejectsConflictingFlags(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())
	stubAgentEnv(t)

	res := runRoot(t, "skill", "install", "--target", "claude-global", "--all")
	if res.Err == nil {
		t.Fatal("expected error when --target and --all are both set")
	}
	if got := errorCode(res.Err); got != codeInvalidInput {
		t.Errorf("expected codeInvalidInput, got %q", got)
	}
}

// TestSkillInstall_UnknownTarget: a typo'd --target surfaces as
// invalid input.
func TestSkillInstall_UnknownTarget(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())
	stubAgentEnv(t)

	res := runRoot(t, "skill", "install", "--target", "bogus-agent")
	if res.Err == nil {
		t.Fatal("expected error for unknown --target")
	}
	if got := errorCode(res.Err); got != codeInvalidInput {
		t.Errorf("expected codeInvalidInput, got %q", got)
	}
}

// TestSkillInstall_JSON: --json emits skill_path + links array.
func TestSkillInstall_JSON(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())
	home := stubAgentEnv(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	res := runRoot(t, "skill", "install", "--json")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	m := decodeJSON(t, res.Stdout.Bytes())
	if got, _ := m["skill_path"].(string); got == "" {
		t.Errorf("skill_path missing, got %v", m)
	}
	links, _ := m["links"].([]any)
	if len(links) != 1 {
		t.Fatalf("expected 1 link in JSON, got %v", m["links"])
	}
}

// TestSkillPrint dumps SKILL.md verbatim to stdout.
func TestSkillPrint(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())

	res := runRoot(t, "skill", "print")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	out := res.Stdout.String()
	if !strings.HasPrefix(out, "---\n") {
		t.Errorf("expected raw markdown starting with frontmatter, got %q", out[:min(40, len(out))])
	}
	if !strings.Contains(out, "name: emailable") {
		t.Errorf("frontmatter missing name, got %q", out)
	}
}

// TestBareSkill_FallsBackToHelpWithoutTTY: with no PTY (the buffer
// stdout in tests), the wizard should be skipped and help shown —
// scripts piping the CLI shouldn't hang on a bubbletea program waiting
// for keys.
func TestBareSkill_FallsBackToHelpWithoutTTY(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())
	stubAgentEnv(t)

	res := runRoot(t, "skill")
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	if !strings.Contains(res.Stdout.String(), "USAGE") {
		t.Errorf("expected help fallback, got %q", res.Stdout.String())
	}
}

// TestSkillTargets_JSON: machine-readable target listing for scripts.
func TestSkillTargets_JSON(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())
	stubAgentEnv(t)

	res := runRoot(t, "skill", "targets", "--json")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	m := decodeJSON(t, res.Stdout.Bytes())
	targets, _ := m["targets"].([]any)
	ids := map[string]bool{}
	for _, t := range targets {
		row, _ := t.(map[string]any)
		if id, _ := row["id"].(string); id != "" {
			ids[id] = true
		}
	}
	for _, want := range []string{"agents-shared", "claude-global", "codex-global"} {
		if !ids[want] {
			t.Errorf("expected target %q in JSON output, got %v", want, ids)
		}
	}
}
