// Package config reads the emailable-cli non-secret config file, holding
// backend routing and output preferences. It has two scopes — a global file
// at $XDG_CONFIG_HOME/emailable/config.json and a project file at
// <project>/.emailable/config.json — both user-managed (the CLI never writes
// them) and sharing the same schema. Credentials live in internal/credentials.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const appDir = "emailable"

// Config is the on-disk schema. All fields are optional. APIURL and OAuthURL
// must be set together within a single file.
type Config struct {
	APIURL   string `json:"api_url,omitempty"`
	OAuthURL string `json:"oauth_url,omitempty"`

	// Output is the default output format ("human" or "json"); unrecognized
	// values fall back to "human".
	Output string `json:"output,omitempty"`
}

// DefaultPath returns the global config path: $XDG_CONFIG_HOME/emailable/config.json,
// falling back to $HOME/.config/emailable/config.json.
//
// Unlike credentials, this path is not env-suffixed — its contents are what
// determine the active env in the first place.
func DefaultPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, appDir, "config.json"), nil
}

// Load returns an empty *Config when path doesn't exist or is zero bytes,
// so a fresh install or a `touch`ed file falls through to defaults.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return &Config{}, nil
	}

	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return &c, nil
}
