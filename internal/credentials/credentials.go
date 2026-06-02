// Package credentials reads and writes the global credentials file.
// The file is environment-suffixed so tokens for different backends don't collide.
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

// Credentials is the on-disk credentials schema for a single environment.
type Credentials struct {
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitzero"`
	OwnerEmail   string    `json:"owner_email,omitempty"`
	APIKey       string    `json:"api_key,omitempty"`
}

func fileName(envName string) string {
	if envName == "" || envName == "default" {
		return "credentials.json"
	}
	return "credentials." + envName + ".json"
}

// DefaultPath returns the credentials file path for the given environment name.
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

// Load reads credentials from path, returning empty credentials if the file does not exist.
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

// Save atomically writes c to path, creating parent directories as needed.
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

// Clear removes the credentials file at path, ignoring a not-found error.
func Clear(path string) error {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("credentials: remove %s: %w", path, err)
	}
	return nil
}
