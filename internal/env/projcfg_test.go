package env

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestFindProjectConfig(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T) (startDir, stopAt, wantPath string, wantFound bool)
	}{
		{
			name: "file in cwd",
			setup: func(t *testing.T) (string, string, string, bool) {
				home := t.TempDir()
				start := filepath.Join(home, "proj")
				cfg := filepath.Join(start, projectConfigDir, projectConfigFilename)
				writeFile(t, cfg, `{"api_url":"x","oauth_url":"y"}`)
				return start, home, cfg, true
			},
		},
		{
			name: "file two levels up",
			setup: func(t *testing.T) (string, string, string, bool) {
				home := t.TempDir()
				cfg := filepath.Join(home, "proj", projectConfigDir, projectConfigFilename)
				start := filepath.Join(home, "proj", "sub", "deeper")
				writeFile(t, cfg, `{"api_url":"x","oauth_url":"y"}`)
				if err := os.MkdirAll(start, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				return start, home, cfg, true
			},
		},
		{
			name: "file at stopAt (home) inclusive",
			setup: func(t *testing.T) (string, string, string, bool) {
				home := t.TempDir()
				cfg := filepath.Join(home, projectConfigDir, projectConfigFilename)
				start := filepath.Join(home, "a", "b")
				writeFile(t, cfg, `{"api_url":"x","oauth_url":"y"}`)
				if err := os.MkdirAll(start, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				return start, home, cfg, true
			},
		},
		{
			name: "no file anywhere",
			setup: func(t *testing.T) (string, string, string, bool) {
				home := t.TempDir()
				start := filepath.Join(home, "proj")
				if err := os.MkdirAll(start, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				return start, home, "", false
			},
		},
		{
			name: "does not walk past stopAt",
			setup: func(t *testing.T) (string, string, string, bool) {
				// Config sits above stopAt; walk-up must stop and not find it.
				root := t.TempDir()
				abovePath := filepath.Join(root, projectConfigDir, projectConfigFilename)
				writeFile(t, abovePath, `{"api_url":"x","oauth_url":"y"}`)
				stopAt := filepath.Join(root, "home")
				start := filepath.Join(stopAt, "proj")
				if err := os.MkdirAll(start, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				return start, stopAt, "", false
			},
		},
		{
			name: "cwd outside home walks to root",
			setup: func(t *testing.T) (string, string, string, bool) {
				// Real-world analogue: a repo cloned outside $HOME. The
				// walk should not stop at $HOME (it's irrelevant) — it
				// should walk up toward filesystem root and find configs
				// along the way.
				base := t.TempDir()
				outside := filepath.Join(base, "elsewhere", "repo")
				cfg := filepath.Join(base, "elsewhere", projectConfigDir, projectConfigFilename)
				writeFile(t, cfg, `{"api_url":"x","oauth_url":"y"}`)
				if err := os.MkdirAll(outside, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				home := filepath.Join(base, "home", "user")
				if err := os.MkdirAll(home, 0o755); err != nil {
					t.Fatalf("mkdir home: %v", err)
				}
				return outside, home, cfg, true
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			start, stop, wantPath, wantFound := tc.setup(t)
			got, ok := findProjectConfig(start, stop)
			if ok != wantFound {
				t.Fatalf("found: got %v, want %v (got path %q)", ok, wantFound, got)
			}
			if wantFound && got != wantPath {
				t.Errorf("path: got %q, want %q", got, wantPath)
			}
		})
	}
}

func TestLoadProjectConfig_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, projectConfigDir, projectConfigFilename)
	writeFile(t, path, `{"api_url":"https://api.example.test/v1","oauth_url":"https://app.example.test"}`)

	cfg, err := loadProjectConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.APIURL != "https://api.example.test/v1" {
		t.Errorf("api: got %q", cfg.APIURL)
	}
	if cfg.OAuthURL != "https://app.example.test" {
		t.Errorf("oauth: got %q", cfg.OAuthURL)
	}
}

func TestLoadProjectConfig_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, projectConfigDir, projectConfigFilename)
	writeFile(t, path, "")

	cfg, err := loadProjectConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.APIURL != "" || cfg.OAuthURL != "" {
		t.Errorf("empty file should yield empty URLs, got api=%q oauth=%q", cfg.APIURL, cfg.OAuthURL)
	}
}

func TestLoadProjectConfig_HalfSet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, projectConfigDir, projectConfigFilename)
	writeFile(t, path, `{"api_url":"https://api.example.test/v1"}`)

	_, err := loadProjectConfig(path)
	if err == nil {
		t.Fatal("expected error for half-set config")
	}
	if !strings.Contains(err.Error(), "both be set") {
		t.Errorf("error %q should mention both must be set", err.Error())
	}
}

func TestLoadProjectConfig_Malformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, projectConfigDir, projectConfigFilename)
	writeFile(t, path, `{"api_url": [unterminated`)

	_, err := loadProjectConfig(path)
	if err == nil {
		t.Fatal("expected error for malformed json")
	}
}

func TestLoadProjectConfig_Missing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.json")

	cfg, err := loadProjectConfig(path)
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if cfg.APIURL != "" || cfg.OAuthURL != "" {
		t.Errorf("missing file should yield empty cfg, got %+v", cfg)
	}
}
