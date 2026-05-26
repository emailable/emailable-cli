package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultPath_XDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/config")

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}

	want := filepath.Join("/custom/config", "emailable", "config.json")
	if got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestDefaultPath_HomeFallback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/tmp/fake-home")

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}

	want := filepath.Join("/tmp/fake-home", ".config", "emailable", "config.json")
	if got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestLoad_FileNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg == nil {
		t.Fatal("Load returned a nil *Config for a missing file")
	}
	if cfg.APIURL != "" || cfg.OAuthURL != "" {
		t.Errorf("expected empty config, got %+v", cfg)
	}
}

func TestLoad_EmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.APIURL != "" || cfg.OAuthURL != "" {
		t.Errorf("expected empty config from zero-byte file, got %+v", cfg)
	}
}

func TestLoad_ValidURLs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body, _ := json.Marshal(&Config{
		APIURL:   "https://api.example.test/v1",
		OAuthURL: "https://app.example.test",
	})
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.APIURL != "https://api.example.test/v1" {
		t.Errorf("APIURL: got %q", cfg.APIURL)
	}
	if cfg.OAuthURL != "https://app.example.test" {
		t.Errorf("OAuthURL: got %q", cfg.OAuthURL)
	}
}

func TestLoad_MalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "config: parse") {
		t.Errorf("expected wrapped 'config: parse' error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("expected error to include path %q, got %q", path, err.Error())
	}
}
