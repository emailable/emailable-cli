// Package credentials reads and writes the global credentials file.
//
// Credentials are global by design: the CLI's login flow is interactive and
// machine-scoped. Per-project credentials are intentionally not supported —
// use the EMAILABLE_API_KEY environment variable (via direnv or your CI's
// secrets system) for per-project / per-shell API keys.
//
// The file path is environment-suffixed (credentials.json for the default
// env, credentials.<name>.json for any other env) so logging in against an
// overridden backend does not clobber the default-env token.
package credentials

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const (
	appDir = "emailable"

	fileMode os.FileMode = 0o600
	dirMode  os.FileMode = 0o700
)

// Credentials is the on-disk schema for the global credentials file. Two
// auth modes can be persisted:
//
//   - OAuth: AccessToken + RefreshToken + ExpiresAt + OwnerEmail, written
//     by `emailable login` after the device flow and refreshed transparently
//     by the CLI.
//   - API key: APIKey, written by `emailable login --api-key ...` (or by
//     piping a key into `emailable login`).
//
// At most one of the two is meaningful at a time; the schema tolerates any
// field being absent (older versions that didn't write it, an interrupted
// write, or loginWithAPIKey clearing the OAuth fields when saving a key).
type Credentials struct {
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitzero"`
	OwnerEmail   string    `json:"owner_email,omitempty"`
	APIKey       string    `json:"api_key,omitempty"`
}

// fileName returns the credentials file name for envName. The default env
// uses credentials.json; any other env name gets a suffix so logins against
// a different backend do not collide.
func fileName(envName string) string {
	if envName == "" || envName == "default" {
		return "credentials.json"
	}
	return "credentials." + envName + ".json"
}

// DefaultPath honors XDG_CONFIG_HOME, falling back to $HOME/.config. envName
// is the active environment name from env.Current() ("default", "custom").
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

// Load returns an empty *Credentials when path doesn't exist (so a fresh
// install behaves the same as a logged-out one) or when the file is zero
// bytes (so `touch` or an interrupted login still parses).
func Load(path string) (*Credentials, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Credentials{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("credentials: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return &Credentials{}, nil
	}

	var c Credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("credentials: parse %s: %w", path, err)
	}
	return &c, nil
}

// Save writes c to path atomically. Data is written to a temp file in the
// same directory with mode 0600, then renamed over the target. This prevents
// a partial write from corrupting an existing file and forces 0600 perms
// even when overwriting a file that had broader permissions.
func (c *Credentials) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("credentials: mkdir %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("credentials: marshal: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, "credentials-*.tmp")
	if err != nil {
		return fmt.Errorf("credentials: create temp in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()

	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(fileMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("credentials: chmod temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("credentials: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("credentials: close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("credentials: rename to %s: %w", path, err)
	}
	committed = true
	return nil
}

// Clear removes the credentials file at path. No-op when the file is absent
// so `logout` is idempotent.
func Clear(path string) error {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("credentials: remove %s: %w", path, err)
	}
	return nil
}
