// Package skill installs the embedded SKILL.md into agent skill dirs.
// Canonical copy lives at ~/.agents/skills/emailable/; other targets
// symlink back so re-installing on upgrade touches one place.
package skill

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/emailable/emailable-cli/skills"
)

const (
	Name              = "emailable"
	FileName          = "SKILL.md"
	canonicalTargetID = "agents-shared"
)

// Location is one known install target. Dir may begin with "~/".
// Detect, when non-nil, gates auto-linking on whether the agent looks
// installed.
type Location struct {
	ID     string
	Name   string
	Dir    string
	Global bool
	Detect func() bool
}

// Targets is recomputed each call so CODEX_HOME and CWD-relative
// project paths reflect current state.
func Targets() []Location {
	return []Location{
		{ID: canonicalTargetID, Name: "Agents (Shared)", Dir: "~/.agents/skills/" + Name, Global: true, Detect: dirExists("~/.agents")},
		{ID: "claude-global", Name: "Claude Code (Global)", Dir: "~/.claude/skills/" + Name, Global: true, Detect: dirExists("~/.claude")},
		{ID: "claude-project", Name: "Claude Code (Project)", Dir: ".claude/skills/" + Name, Global: false, Detect: dirExists(".claude")},
		{ID: "opencode-global", Name: "OpenCode (Global)", Dir: "~/.config/opencode/skill/" + Name, Global: true, Detect: dirExists("~/.config/opencode")},
		{ID: "opencode-project", Name: "OpenCode (Project)", Dir: ".opencode/skill/" + Name, Global: false, Detect: dirExists(".opencode")},
		{ID: "codex-global", Name: "Codex (Global)", Dir: codexGlobalDir(), Global: true, Detect: dirExists(codexGlobalParent())},
	}
}

func LookupTarget(id string) (Location, bool) {
	for _, t := range Targets() {
		if t.ID == id {
			return t, true
		}
	}
	return Location{}, false
}

func Canonical() Location {
	loc, _ := LookupTarget(canonicalTargetID)
	return loc
}

// Content panics on miss — go:embed verifies at compile time, so a
// runtime read failure is a broken build.
func Content() string {
	data, err := skills.FS.ReadFile(Name + "/" + FileName)
	if err != nil {
		panic(fmt.Errorf("read embedded skill: %w", err))
	}
	return string(data)
}

type Result struct {
	SkillPath string
	Links     []LinkResult
}

type LinkResult struct {
	Target Location
	Path   string
	Notice string // non-empty when symlink fell back to a copy
}

func InstallToDir(dir string) (string, error) {
	abs, err := Expand(dir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", fmt.Errorf("create skill dir: %w", err)
	}
	file := filepath.Join(abs, FileName)
	if err := os.WriteFile(file, []byte(Content()), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", FileName, err)
	}
	return file, nil
}

func InstallToFile(path string) (string, error) {
	abs, err := Expand(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", fmt.Errorf("create parent dir: %w", err)
	}
	if err := os.WriteFile(abs, []byte(Content()), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", filepath.Base(abs), err)
	}
	return abs, nil
}

// NormalizeCustomPath turns a free-form path into a SKILL.md file
// path: .md verbatim, /emailable[/] gets SKILL.md appended, anything
// else gets emailable/SKILL.md appended.
func NormalizeCustomPath(input string) string {
	p := strings.TrimSpace(input)
	if strings.HasSuffix(strings.ToLower(p), ".md") {
		return p
	}
	p = strings.TrimRight(p, "/\\")
	if strings.HasSuffix(p, string(filepath.Separator)+Name) || strings.HasSuffix(p, "/"+Name) || p == Name {
		return filepath.Join(p, FileName)
	}
	return filepath.Join(p, Name, FileName)
}

func InstallCanonical() (string, error) {
	return InstallToDir(Canonical().Dir)
}

// InstallDetected links only global agents whose dirs already exist.
// Project targets are skipped — CWD may not be the project the user
// meant.
func InstallDetected() (Result, error) {
	return installMany(func(t Location) bool {
		return t.Global && t.ID != canonicalTargetID && (t.Detect == nil || t.Detect())
	})
}

// InstallAll links every global target, detected or not.
func InstallAll() (Result, error) {
	return installMany(func(t Location) bool {
		return t.Global && t.ID != canonicalTargetID
	})
}

func InstallOne(target Location) (Result, error) {
	return installMany(func(t Location) bool {
		return t.ID == target.ID && t.ID != canonicalTargetID
	})
}

func installMany(keep func(Location) bool) (Result, error) {
	res := Result{}
	path, err := InstallCanonical()
	if err != nil {
		return res, err
	}
	res.SkillPath = path
	for _, t := range Targets() {
		if !keep(t) {
			continue
		}
		link, err := linkToCanonical(t)
		if err != nil {
			return res, err
		}
		res.Links = append(res.Links, link)
	}
	return res, nil
}

// linkToCanonical symlinks target.Dir → canonical, with a SKILL.md
// copy fallback for hosts that can't symlink (unprivileged Windows).
// Assumes InstallCanonical already ran.
func linkToCanonical(target Location) (LinkResult, error) {
	targetDir, err := Expand(target.Dir)
	if err != nil {
		return LinkResult{}, err
	}
	canonicalDir, err := Expand(Canonical().Dir)
	if err != nil {
		return LinkResult{}, err
	}
	if targetDir == canonicalDir {
		return LinkResult{Target: target, Path: canonicalDir}, nil
	}
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		return LinkResult{}, fmt.Errorf("create symlink parent: %w", err)
	}
	// Re-install must converge: drop whatever's there (file, dir, or symlink).
	_ = os.RemoveAll(targetDir)
	if err := os.Symlink(canonicalDir, targetDir); err == nil {
		return LinkResult{Target: target, Path: targetDir}, nil
	} else {
		if mkErr := os.MkdirAll(targetDir, 0o755); mkErr != nil {
			return LinkResult{}, fmt.Errorf("create symlink dir (copy fallback): %w", mkErr)
		}
		src := filepath.Join(canonicalDir, FileName)
		dst := filepath.Join(targetDir, FileName)
		if cpErr := copyFile(src, dst); cpErr != nil {
			return LinkResult{}, fmt.Errorf("symlink failed (%w) and copy fallback also failed: %w", err, cpErr)
		}
		return LinkResult{
			Target: target,
			Path:   targetDir,
			Notice: fmt.Sprintf("symlinks unsupported here (%v); copied SKILL.md instead", err),
		}, nil
	}
}

// Expand resolves "~/" and returns an absolute path.
func Expand(path string) (string, error) {
	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return home, nil
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[2:])
	}
	return filepath.Abs(path)
}

func codexGlobalDir() string {
	if codex := strings.TrimSpace(os.Getenv("CODEX_HOME")); codex != "" {
		return filepath.Join(codex, "skills", Name)
	}
	return "~/.codex/skills/" + Name
}

func codexGlobalParent() string {
	if codex := strings.TrimSpace(os.Getenv("CODEX_HOME")); codex != "" {
		return codex
	}
	return "~/.codex"
}

func dirExists(path string) func() bool {
	return func() bool {
		abs, err := Expand(path)
		if err != nil {
			return false
		}
		info, err := os.Stat(abs)
		if err != nil {
			return false
		}
		return info.IsDir()
	}
}

func copyFile(src, dst string) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()
	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer d.Close()
	_, err = io.Copy(d, s)
	return err
}
