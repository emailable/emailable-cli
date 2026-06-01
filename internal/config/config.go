// Package config reads the non-secret config file (backend URLs, output format). The CLI never writes it.
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

// Config holds non-secret configuration (backend URLs and output format).
type Config struct {
	APIURL   string `json:"api_url,omitempty"`
	OAuthURL string `json:"oauth_url,omitempty"`

	// Output is the default output format ("human" or "json"); unrecognized
	// values fall back to "human".
	Output string `json:"output,omitempty"`
}

// DefaultPath is not env-suffixed — its contents are what determine the active env in the first place.
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

// Load reads a Config from path, returning an empty Config if the file does not exist.
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
