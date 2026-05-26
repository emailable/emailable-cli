package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/emailable/emailable-cli/internal/config"
)

// TestLogin_APIKey_HappyPath validates the --api-key path: the CLI calls
// /v1/account to confirm the key works, persists the key + owner_email to
// config, and surfaces a success message.
func TestLogin_APIKey_HappyPath(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/account" {
			http.NotFound(w, r)
			return
		}
		// Confirm the Bearer token is the key we just supplied.
		if got := r.Header.Get("Authorization"); got != "Bearer sk_login_xxx" {
			t.Errorf("expected Bearer sk_login_xxx, got %q", got)
		}
		writeJSON(w, map[string]any{
			"owner_email":       "owner@example.com",
			"available_credits": 100,
		})
	}))

	res := runRoot(t, "login", "--api-key", "sk_login_xxx")
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	if !strings.Contains(res.Stdout.String(), "owner@example.com") {
		t.Errorf("expected owner_email in success message, got %q", res.Stdout.String())
	}

	// Verify persistence.
	cfg, err := config.Load(env.ConfigPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.APIKey != "sk_login_xxx" {
		t.Errorf("APIKey not saved: %+v", cfg)
	}
	if cfg.OwnerEmail != "owner@example.com" {
		t.Errorf("OwnerEmail not saved: %+v", cfg)
	}
}

// TestLogin_APIKey_RejectedBy401: a 401 from /v1/account must NOT persist
// the bad key.
func TestLogin_APIKey_RejectedBy401(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "bad key")
	}))

	res := runRoot(t, "login", "--api-key", "sk_bad")
	if res.Err == nil {
		t.Fatal("expected error")
	}
	if got := errorCode(res.Err); got != codeNotAuthenticated {
		t.Errorf("errorCode: got %q want %q", got, codeNotAuthenticated)
	}

	// Key must NOT have landed on disk.
	cfg, err := config.Load(env.ConfigPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.APIKey != "" {
		t.Errorf("APIKey should not be persisted on validation failure, got %q", cfg.APIKey)
	}
}

// TestLogin_APIKey_ClearsOAuth verifies that logging in with an API key
// supersedes any prior OAuth credentials.
func TestLogin_APIKey_ClearsOAuth(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"owner_email":       "owner@example.com",
			"available_credits": 1,
		})
	}))
	// Pre-seed OAuth tokens.
	prior := &config.Config{
		AccessToken:  "stale_at",
		RefreshToken: "stale_rt",
	}
	if err := prior.Save(env.ConfigPath); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res := runRoot(t, "login", "--api-key", "sk_new")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}

	cfg, err := config.Load(env.ConfigPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.APIKey != "sk_new" {
		t.Errorf("APIKey: got %q", cfg.APIKey)
	}
	if cfg.AccessToken != "" || cfg.RefreshToken != "" {
		t.Errorf("expected OAuth tokens to be cleared, got %+v", cfg)
	}
}

// TestApiKeyForLogin_FlagWins exercises the helper directly: when the flag
// is set, stdin shouldn't be consulted.
func TestApiKeyForLogin_FlagWins(t *testing.T) {
	prev := apiKey
	apiKey = "  sk_flag  "
	t.Cleanup(func() { apiKey = prev })

	key, ok := apiKeyForLogin()
	if !ok {
		t.Fatal("expected ok=true")
	}
	if key != "sk_flag" {
		t.Errorf("expected trimmed key, got %q", key)
	}
}
