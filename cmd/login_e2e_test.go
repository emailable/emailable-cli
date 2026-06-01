package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/emailable/emailable-cli/internal/credentials"
)

// TestLogin_APIKey_HappyPath validates the --api-key path: the CLI calls
// /v1/account to confirm the key works, persists the key + owner_email to
// the credentials file, and surfaces a success message.
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
	creds, err := credentials.Load(env.CredentialsPath)
	if err != nil {
		t.Fatalf("load credentials: %v", err)
	}
	if creds.APIKey != "sk_login_xxx" {
		t.Errorf("APIKey not saved: %+v", creds)
	}
	if creds.OwnerEmail != "owner@example.com" {
		t.Errorf("OwnerEmail not saved: %+v", creds)
	}
}

func TestLogin_APIKey_QuietSuppressesSuccess(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"owner_email":       "owner@example.com",
			"available_credits": 100,
		})
	}))

	res := runRoot(t, "login", "--api-key", "sk_login_xxx", "--quiet")
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	if strings.TrimSpace(res.Stdout.String()) != "" {
		t.Errorf("expected quiet login to suppress success stdout, got %q", res.Stdout.String())
	}

	creds, err := credentials.Load(env.CredentialsPath)
	if err != nil {
		t.Fatalf("load credentials: %v", err)
	}
	if creds.APIKey != "sk_login_xxx" {
		t.Errorf("APIKey not saved: %+v", creds)
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
	creds, err := credentials.Load(env.CredentialsPath)
	if err != nil {
		t.Fatalf("load credentials: %v", err)
	}
	if creds.APIKey != "" {
		t.Errorf("APIKey should not be persisted on validation failure, got %q", creds.APIKey)
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
	prior := &credentials.Credentials{
		AccessToken:  "stale_at",
		RefreshToken: "stale_rt",
	}
	if err := prior.Save(env.CredentialsPath); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res := runRoot(t, "login", "--api-key", "sk_new")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}

	creds, err := credentials.Load(env.CredentialsPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if creds.APIKey != "sk_new" {
		t.Errorf("APIKey: got %q", creds.APIKey)
	}
	if creds.AccessToken != "" || creds.RefreshToken != "" {
		t.Errorf("expected OAuth tokens to be cleared, got %+v", creds)
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
