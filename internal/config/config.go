// Package config reads and writes the emailable-cli config file.
//
// The config holds a single set of OAuth credentials. The file path is
// environment-suffixed (config.yml for the default env, config.<name>.yml
// for any other env) so a login against an overridden backend does not
// clobber the default token.
//
// Environment URLs and OAuth client_id are resolved at runtime (see
// internal/env), not stored in the config file.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	appDir = "emailable"

	configFileMode os.FileMode = 0o600
	configDirMode  os.FileMode = 0o700
)

// Config holds the credentials for the active environment.
//
// Two auth modes can be persisted:
//
//   - OAuth: AccessToken + RefreshToken + ExpiresAt + OwnerEmail, written
//     by `emailable login` after the device flow.
//   - API key: APIKey, written by `emailable login --api-key ...` (or by
//     piping a key into `emailable login`).
//
// At most one of the two is meaningful at a time, but the schema tolerates
// either being absent without erroring so logout can clear individual
// fields without rewriting the file.
type Config struct {
	AccessToken  string    `yaml:"access_token,omitempty"`
	RefreshToken string    `yaml:"refresh_token,omitempty"`
	ExpiresAt    time.Time `yaml:"expires_at,omitempty"`
	OwnerEmail   string    `yaml:"owner_email,omitempty"`
	APIKey       string    `yaml:"api_key,omitempty"`
}

// fileName returns the credentials file name for envName. The default env
// uses config.yml; any other env name gets a suffix so logins against a
// different backend do not collide.
func fileName(envName string) string {
	if envName == "" || envName == "default" {
		return "config.yml"
	}
	return "config." + envName + ".yml"
}

// DefaultPath honors XDG_CONFIG_HOME, falling back to $HOME/.config. envName
// is the active environment name from env.Current() ("default", "custom", ...).
func DefaultPath(envName string) (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, appDir, fileName(envName)), nil
}

// Load returns an empty *Config when path doesn't exist, so a fresh install
// behaves the same as a logged-out one.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return &c, nil
}

// Save writes c to path atomically. Data is written to a temp file in the
// same directory with mode 0600, then renamed over the target. This prevents
// a partial write from corrupting an existing config and forces 0600 perms
// even when overwriting a file that had broader permissions.
func (c *Config) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, configDirMode); err != nil {
		return fmt.Errorf("config: mkdir %s: %w", dir, err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "config-*.tmp")
	if err != nil {
		return fmt.Errorf("config: create temp in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()

	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(configFileMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: chmod temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("config: rename to %s: %w", path, err)
	}
	committed = true
	return nil
}

// Clear removes the config file at path. No-op when the file is absent so
// `logout` is idempotent.
func Clear(path string) error {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("config: remove %s: %w", path, err)
	}
	return nil
}
