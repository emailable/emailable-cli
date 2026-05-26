package env

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// projectConfigFilename is the name of the project-local config file the CLI
// looks for. It's intentionally dotfile-prefixed so it sorts with other
// per-repo config (.envrc, .gitignore) and is easy to gitignore.
const projectConfigFilename = ".emailable.yml"

// projectConfig is the on-disk schema for .emailable.yml. Both fields are
// optional individually but must be set together — loadProjectConfig enforces
// that.
type projectConfig struct {
	APIURL   string `yaml:"api_url"`
	OAuthURL string `yaml:"oauth_url"`
}

// findProjectConfig walks up from startDir looking for .emailable.yml.
// Returns (path, found) where found=false means we walked all the way to the
// stopping point without finding the file.
//
// startDir is typically os.Getwd(). stopAt is the directory to stop walking
// at (typically the user's home dir). Walking continues past stopAt to the
// filesystem root only if startDir is not a descendant of stopAt — that lets
// a checkout living outside $HOME still find a config without leaking
// upward into siblings of the user's home.
//
// stopAt is a parameter (rather than always being $HOME) so tests can
// inject a sandbox root.
func findProjectConfig(startDir, stopAt string) (string, bool) {
	// Normalize so the equality check below is reliable.
	startDir = filepath.Clean(startDir)
	stopAt = filepath.Clean(stopAt)

	// Decide where to stop. If startDir is inside stopAt, stop at stopAt
	// (inclusive). Otherwise, walk all the way to the filesystem root.
	stopInclusive := isDescendantOrEqual(startDir, stopAt)

	dir := startDir
	for {
		candidate := filepath.Join(dir, projectConfigFilename)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}

		if stopInclusive && dir == stopAt {
			return "", false
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root.
			return "", false
		}
		dir = parent
	}
}

// isDescendantOrEqual reports whether child is the same path as parent or
// nested somewhere underneath it. Both inputs should already be cleaned.
func isDescendantOrEqual(child, parent string) bool {
	if child == parent {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	// filepath.Rel returns ".." or paths starting with "../" when child
	// escapes parent.
	if rel == ".." || len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator) {
		return false
	}
	return true
}

// loadProjectConfig parses a discovered .emailable.yml file. Returns the
// api_url and oauth_url, or an error if the file is malformed or only one
// of the two URLs is set (they must be set together, matching the env-var
// rule).
func loadProjectConfig(path string) (apiURL, oauthURL string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read: %w", err)
	}

	var cfg projectConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return "", "", fmt.Errorf("parse yaml: %w", err)
	}

	// Empty file → treat as "nothing configured here", caller will fall
	// through to defaults. Half-set → error, matches env-var semantics.
	if cfg.APIURL == "" && cfg.OAuthURL == "" {
		return "", "", nil
	}
	if cfg.APIURL == "" || cfg.OAuthURL == "" {
		return "", "", fmt.Errorf("api_url and oauth_url must both be set")
	}

	return cfg.APIURL, cfg.OAuthURL, nil
}

// findProjectConfigFromCWD is the production wrapper around findProjectConfig:
// it resolves os.Getwd() and the user's home directory and delegates. Returns
// found=false (without error) if either lookup fails — a missing cwd or
// unknown home shouldn't crash the CLI, it should just mean "no project
// config available".
func findProjectConfigFromCWD() (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// Fall back to root so we still walk the tree, just without the
		// home-dir stopping point.
		home = string(filepath.Separator)
	}
	return findProjectConfig(cwd, home)
}
