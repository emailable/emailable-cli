package env

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/emailable/emailable-cli/internal/config"
)

// Project-local config lives at <dir>/.emailable/config.json. The
// dotfile-prefixed dir keeps it tidy in repo roots and avoids colliding with
// the many unrelated tools that ship a bare config.json.
const (
	projectConfigDir      = ".emailable"
	projectConfigFilename = "config.json"
)

// findProjectConfig walks up from startDir looking for .emailable/config.json.
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
	startDir = filepath.Clean(startDir)
	stopAt = filepath.Clean(stopAt)

	stopInclusive := isDescendantOrEqual(startDir, stopAt)

	dir := startDir
	for {
		candidate := filepath.Join(dir, projectConfigDir, projectConfigFilename)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}

		if stopInclusive && dir == stopAt {
			return "", false
		}

		parent := filepath.Dir(dir)
		if parent == dir {
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
	if rel == ".." || len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator) {
		return false
	}
	return true
}

// loadProjectConfig parses a discovered .emailable/config.json file. Returns
// an error if the file is malformed or has only one of the two URLs set (they
// must be set together within a single file).
func loadProjectConfig(path string) (*config.Config, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}

	// Half-set URLs within a single file are a user mistake; both must be set
	// together.
	if (cfg.APIURL == "") != (cfg.OAuthURL == "") {
		return nil, fmt.Errorf("api_url and oauth_url must both be set")
	}

	return cfg, nil
}

// ProjectConfigPath finds the project-local .emailable/config.json by walking
// up from the current working directory. Returns ("", false) when none found.
func ProjectConfigPath() (string, bool) {
	return findProjectConfigFromCWD()
}

// findProjectConfigFromCWD resolves os.Getwd() and the user's home directory
// and delegates to findProjectConfig. Returns found=false (without error) if
// either lookup fails — a missing cwd or unknown home shouldn't crash the
// CLI, it should just mean "no project config available".
func findProjectConfigFromCWD() (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = string(filepath.Separator)
	}
	return findProjectConfig(cwd, home)
}
