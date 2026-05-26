package credentials

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultPath_Default_XDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/config")

	got, err := DefaultPath("default")
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}

	want := filepath.Join("/custom/config", "emailable", "credentials.json")
	if got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestDefaultPath_Custom_HomeFallback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/tmp/fake-home")

	got, err := DefaultPath("custom")
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}

	want := filepath.Join("/tmp/fake-home", ".config", "emailable", "credentials.custom.json")
	if got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestDefaultPath_EmptyEnvTreatedAsDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/config")

	got, err := DefaultPath("")
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}

	want := filepath.Join("/custom/config", "emailable", "credentials.json")
	if got != want {
		t.Errorf("DefaultPath(\"\") = %q, want %q", got, want)
	}
}

func TestLoad_FileNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c == nil {
		t.Fatal("Load returned a nil *Credentials for a missing file")
	}
	if c.AccessToken != "" {
		t.Errorf("expected empty AccessToken on fresh credentials, got %q", c.AccessToken)
	}
}

func TestCredentials_JSONSchema(t *testing.T) {
	c := Credentials{
		AccessToken:  "test-access",
		RefreshToken: "test-refresh",
		ExpiresAt:    time.Date(2026, 5, 14, 15, 0, 0, 0, time.UTC),
		OwnerEmail:   "user@example.com",
	}

	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(data)

	wantSubstrings := []string{
		`"access_token":"test-access"`,
		`"refresh_token":"test-refresh"`,
		`"owner_email":"user@example.com"`,
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("expected JSON to contain %q, got:\n%s", want, got)
		}
	}
}

func TestCredentials_OmitsEmptyFields(t *testing.T) {
	c := Credentials{AccessToken: "only-this"}

	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(data)

	if !strings.Contains(got, `"access_token":"only-this"`) {
		t.Errorf("expected access_token in output, got:\n%s", got)
	}

	omitted := []string{"refresh_token", "owner_email", "api_key", "expires_at"}
	for _, key := range omitted {
		if strings.Contains(got, `"`+key+`"`) {
			t.Errorf("expected %s to be omitted, got:\n%s", key, got)
		}
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")

	original := &Credentials{
		AccessToken:  "access-123",
		RefreshToken: "refresh-456",
		ExpiresAt:    time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC),
		OwnerEmail:   "user@example.com",
	}

	if err := original.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.AccessToken != original.AccessToken {
		t.Errorf("AccessToken: got %q, want %q", loaded.AccessToken, original.AccessToken)
	}
	if loaded.RefreshToken != original.RefreshToken {
		t.Errorf("RefreshToken: got %q, want %q", loaded.RefreshToken, original.RefreshToken)
	}
	if loaded.OwnerEmail != original.OwnerEmail {
		t.Errorf("OwnerEmail: got %q, want %q", loaded.OwnerEmail, original.OwnerEmail)
	}
	if !loaded.ExpiresAt.Equal(original.ExpiresAt) {
		t.Errorf("ExpiresAt: got %v, want %v", loaded.ExpiresAt, original.ExpiresAt)
	}
}

func TestSave_CreatesParentDir(t *testing.T) {
	nested := filepath.Join(t.TempDir(), "nested", "deep", "dirs")
	path := filepath.Join(nested, "credentials.json")

	c := &Credentials{AccessToken: "x"}
	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file at %s, got error: %v", path, err)
	}
}

func TestSave_FilePerms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")

	c := &Credentials{AccessToken: "x"}
	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("file perms: got %o, want 600", got)
	}
}

func TestLoad_MalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "credentials: parse") {
		t.Errorf("expected wrapped 'credentials: parse' error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("expected error to include path %q, got %q", path, err.Error())
	}
}

func TestSave_ReadOnlyParentDir(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatalf("setup chmod: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(parent, 0o700)
	})

	path := filepath.Join(parent, "subdir", "credentials.json")
	c := &Credentials{AccessToken: "x"}

	err := c.Save(path)
	if err == nil {
		t.Fatal("expected Save to fail with read-only parent, got nil")
	}
	if !strings.Contains(err.Error(), "credentials:") {
		t.Errorf("expected wrapped 'credentials:' error, got %q", err.Error())
	}
}

func TestSave_NarrowsPermsOnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")

	if err := os.WriteFile(path, []byte(`{"access_token":"existing"}`), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	c := &Credentials{AccessToken: "new"}
	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("file perms after overwrite: got %o, want 600", got)
	}
}

func TestClear_RemovesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")

	c := &Credentials{AccessToken: "x"}
	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := Clear(path); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected file to be gone, stat err: %v", err)
	}
}

func TestClear_MissingFileIsNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "never-existed.json")
	if err := Clear(path); err != nil {
		t.Errorf("Clear on missing file should be no-op, got: %v", err)
	}
}
