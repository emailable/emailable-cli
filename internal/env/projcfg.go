package env

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/emailable/emailable-cli/internal/config"
)

const (
	projectConfigDir      = ".emailable"
	projectConfigFilename = "config.json"
)

// findProjectConfig walks up from startDir looking for .emailable/config.json.
// Walking stops at stopAt only when startDir is a descendant of it, so checkouts
// outside $HOME still find a config without leaking into unrelated dirs.
// stopAt is a parameter so tests can inject a sandbox root.
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

func loadProjectConfig(path string) (*config.Config, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}

	if (cfg.APIURL == "") != (cfg.OAuthURL == "") {
		return nil, fmt.Errorf("api_url and oauth_url must both be set")
	}

	return cfg, nil
}

// ProjectConfigPath returns the nearest project config path found by walking up from the current directory.
func ProjectConfigPath() (string, bool) {
	return findProjectConfigFromCWD()
}

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
