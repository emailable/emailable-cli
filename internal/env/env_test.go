package env

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chdir changes cwd for the duration of t. It captures the prior cwd and
// restores it via t.Cleanup, so each test that uses it stays self-contained.
// Tests in this package are NOT parallel-safe because Current() reads cwd —
// keep them sequential.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(prev); err != nil {
			t.Logf("restore cwd: %v", err)
		}
	})
}

// isolateHome points HOME at a fresh temp dir so findProjectConfigFromCWD
// can't pick up the developer's real ~/.emailable.yml during tests.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func writeProjectConfig(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, ".emailable.yml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestCurrent_DefaultsToProduction(t *testing.T) {
	t.Setenv("EMAILABLE_API_URL", "")
	t.Setenv("EMAILABLE_OAUTH_URL", "")
	home := isolateHome(t)
	chdir(t, home)

	e, err := Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if e.Name != "default" {
		t.Errorf("Name: got %q, want default", e.Name)
	}
	if e.APIBaseURL != DefaultAPIBaseURL {
		t.Errorf("APIBaseURL: got %q, want %q", e.APIBaseURL, DefaultAPIBaseURL)
	}
	if e.OAuthBaseURL != DefaultOAuthBaseURL {
		t.Errorf("OAuthBaseURL: got %q, want %q", e.OAuthBaseURL, DefaultOAuthBaseURL)
	}
	if e.ClientID != PublicClientID {
		t.Errorf("ClientID: got %q, want %q", e.ClientID, PublicClientID)
	}
}

func TestCurrent_CustomWhenBothSet(t *testing.T) {
	t.Setenv("EMAILABLE_API_URL", "https://api.example.test/v1")
	t.Setenv("EMAILABLE_OAUTH_URL", "https://app.example.test")
	home := isolateHome(t)
	chdir(t, home)

	e, err := Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if e.Name != "custom" {
		t.Errorf("Name: got %q, want custom", e.Name)
	}
	if e.APIBaseURL != "https://api.example.test/v1" {
		t.Errorf("APIBaseURL: got %q", e.APIBaseURL)
	}
	if e.OAuthBaseURL != "https://app.example.test" {
		t.Errorf("OAuthBaseURL: got %q", e.OAuthBaseURL)
	}
	if e.ClientID != PublicClientID {
		t.Errorf("ClientID: got %q, want %q", e.ClientID, PublicClientID)
	}
}

func TestCurrent_OnlyAPISetErrors(t *testing.T) {
	t.Setenv("EMAILABLE_API_URL", "https://api.example.test/v1")
	t.Setenv("EMAILABLE_OAUTH_URL", "")
	home := isolateHome(t)
	chdir(t, home)

	_, err := Current()
	if err == nil {
		t.Fatal("expected error when only EMAILABLE_API_URL set, got nil")
	}
	if !strings.Contains(err.Error(), "both be set") {
		t.Errorf("error %q should mention both must be set", err.Error())
	}
}

func TestCurrent_OnlyOAuthSetErrors(t *testing.T) {
	t.Setenv("EMAILABLE_API_URL", "")
	t.Setenv("EMAILABLE_OAUTH_URL", "https://app.example.test")
	home := isolateHome(t)
	chdir(t, home)

	_, err := Current()
	if err == nil {
		t.Fatal("expected error when only EMAILABLE_OAUTH_URL set, got nil")
	}
	if !strings.Contains(err.Error(), "both be set") {
		t.Errorf("error %q should mention both must be set", err.Error())
	}
}

func TestCurrent_EmptyStringsCountAsUnset(t *testing.T) {
	t.Setenv("EMAILABLE_API_URL", "")
	t.Setenv("EMAILABLE_OAUTH_URL", "")
	home := isolateHome(t)
	chdir(t, home)

	e, err := Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if e.Name != "default" {
		t.Errorf("empty strings should yield default env; got %q", e.Name)
	}
}

func TestCurrent_ProjectConfigCustom(t *testing.T) {
	t.Setenv("EMAILABLE_API_URL", "")
	t.Setenv("EMAILABLE_OAUTH_URL", "")
	home := isolateHome(t)
	proj := filepath.Join(home, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeProjectConfig(t, proj,
		"api_url: https://api.example.test/v1\noauth_url: https://app.example.test\n")
	chdir(t, proj)

	e, err := Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if e.Name != "custom" {
		t.Errorf("Name: got %q, want custom", e.Name)
	}
	if e.APIBaseURL != "https://api.example.test/v1" {
		t.Errorf("APIBaseURL: got %q", e.APIBaseURL)
	}
	if e.OAuthBaseURL != "https://app.example.test" {
		t.Errorf("OAuthBaseURL: got %q", e.OAuthBaseURL)
	}
}

func TestCurrent_EnvVarsWinOverProjectConfig(t *testing.T) {
	t.Setenv("EMAILABLE_API_URL", "https://env.api.test/v1")
	t.Setenv("EMAILABLE_OAUTH_URL", "https://env.oauth.test")
	home := isolateHome(t)
	proj := filepath.Join(home, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeProjectConfig(t, proj,
		"api_url: https://file.api.test/v1\noauth_url: https://file.oauth.test\n")
	chdir(t, proj)

	e, err := Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if e.APIBaseURL != "https://env.api.test/v1" {
		t.Errorf("env should win; got APIBaseURL=%q", e.APIBaseURL)
	}
	if e.OAuthBaseURL != "https://env.oauth.test" {
		t.Errorf("env should win; got OAuthBaseURL=%q", e.OAuthBaseURL)
	}
}

func TestCurrent_ProjectConfigHalfSetErrors(t *testing.T) {
	t.Setenv("EMAILABLE_API_URL", "")
	t.Setenv("EMAILABLE_OAUTH_URL", "")
	home := isolateHome(t)
	proj := filepath.Join(home, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeProjectConfig(t, proj, "api_url: https://only.api.test/v1\n")
	chdir(t, proj)

	_, err := Current()
	if err == nil {
		t.Fatal("expected error for half-set project config")
	}
	if !strings.Contains(err.Error(), "both be set") {
		t.Errorf("error %q should mention both must be set", err.Error())
	}
}

func TestCurrent_ProjectConfigMalformedErrors(t *testing.T) {
	t.Setenv("EMAILABLE_API_URL", "")
	t.Setenv("EMAILABLE_OAUTH_URL", "")
	home := isolateHome(t)
	proj := filepath.Join(home, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeProjectConfig(t, proj, "api_url: [unterminated\n")
	chdir(t, proj)

	_, err := Current()
	if err == nil {
		t.Fatal("expected error for malformed project config")
	}
	if !strings.Contains(err.Error(), ".emailable.yml") {
		t.Errorf("error %q should reference the offending file path", err.Error())
	}
}

func TestCurrent_ProjectConfigWalksUp(t *testing.T) {
	t.Setenv("EMAILABLE_API_URL", "")
	t.Setenv("EMAILABLE_OAUTH_URL", "")
	home := isolateHome(t)
	proj := filepath.Join(home, "proj")
	deep := filepath.Join(proj, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeProjectConfig(t, proj,
		"api_url: https://walk.api.test/v1\noauth_url: https://walk.oauth.test\n")
	chdir(t, deep)

	e, err := Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if e.APIBaseURL != "https://walk.api.test/v1" {
		t.Errorf("walk-up should find ancestor config; got %q", e.APIBaseURL)
	}
}

func TestCurrent_EmptyProjectConfigFallsThroughToDefault(t *testing.T) {
	t.Setenv("EMAILABLE_API_URL", "")
	t.Setenv("EMAILABLE_OAUTH_URL", "")
	home := isolateHome(t)
	proj := filepath.Join(home, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeProjectConfig(t, proj, "")
	chdir(t, proj)

	e, err := Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if e.Name != "default" {
		t.Errorf("empty project config should yield default env; got %q", e.Name)
	}
}
