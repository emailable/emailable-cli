package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/emailable/emailable-cli/internal/credentials"
	"github.com/emailable/emailable-cli/internal/env"
	"github.com/emailable/emailable-cli/internal/oauth"
)

// Helper: build a cmdCtx directly with the testEnv plumbing. Avoids going
// through newCmdCtx so tests can inject specific credential values.
func newCmdCtxForTest(t *testing.T, creds *credentials.Credentials) *cmdCtx {
	t.Helper()
	e, err := env.Current()
	if err != nil {
		t.Fatalf("env.Current: %v", err)
	}
	path, err := credentials.DefaultPath(e.Name)
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if creds == nil {
		creds = &credentials.Credentials{}
	}
	return &cmdCtx{Env: e, CredentialsPath: path, Credentials: creds}
}

func TestEffectiveAPIKey_EnvBeatsStored(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())
	t.Setenv("EMAILABLE_API_KEY", "from_env")

	c := newCmdCtxForTest(t, &credentials.Credentials{APIKey: "from_stored"})
	key, src := c.effectiveAPIKey()
	if key != "from_env" || src != apiKeySourceEnv {
		t.Errorf("got (%q,%q), want (from_env, env)", key, src)
	}
}

func TestEffectiveAPIKey_Stored(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())

	c := newCmdCtxForTest(t, &credentials.Credentials{APIKey: "stored_key"})
	key, src := c.effectiveAPIKey()
	if key != "stored_key" || src != apiKeySourceStored {
		t.Errorf("got (%q,%q), want (stored_key, stored)", key, src)
	}
}

func TestEffectiveAPIKey_None(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())

	c := newCmdCtxForTest(t, &credentials.Credentials{})
	key, src := c.effectiveAPIKey()
	if key != "" || src != apiKeySourceNone {
		t.Errorf("got (%q,%q), want (\"\", none)", key, src)
	}
}

func TestNeedsRefresh(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())

	cases := []struct {
		name  string
		creds *credentials.Credentials
		want  bool
	}{
		{
			name:  "no refresh token",
			creds: &credentials.Credentials{AccessToken: "at", ExpiresAt: time.Now().Add(-1 * time.Hour)},
			want:  false,
		},
		{
			name:  "no expiry set",
			creds: &credentials.Credentials{RefreshToken: "rt"},
			want:  false,
		},
		{
			name:  "expired",
			creds: &credentials.Credentials{RefreshToken: "rt", ExpiresAt: time.Now().Add(-1 * time.Hour)},
			want:  true,
		},
		{
			name:  "near expiry (within skew)",
			creds: &credentials.Credentials{RefreshToken: "rt", ExpiresAt: time.Now().Add(10 * time.Second)},
			want:  true,
		},
		{
			name:  "fresh",
			creds: &credentials.Credentials{RefreshToken: "rt", ExpiresAt: time.Now().Add(1 * time.Hour)},
			want:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newCmdCtxForTest(t, tc.creds)
			if got := c.needsRefresh(); got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestRequireAuth_APIKeyShortCircuits(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())

	c := newCmdCtxForTest(t, &credentials.Credentials{APIKey: "sk_xxx"})
	client, err := c.requireAuth(context.Background())
	if err != nil {
		t.Fatalf("requireAuth: %v", err)
	}
	if client == nil {
		t.Error("expected non-nil client")
	}
}

func TestRequireAuth_NoCredentials(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())

	c := newCmdCtxForTest(t, &credentials.Credentials{})
	_, err := c.requireAuth(context.Background())
	if !errors.Is(err, errNotAuthenticated) {
		t.Errorf("expected errNotAuthenticated, got %v", err)
	}
}

// TestRequireAuth_OAuthFresh: a stored OAuth token with future expiry should
// build a client without refreshing.
func TestRequireAuth_OAuthFresh(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())

	c := newCmdCtxForTest(t, &credentials.Credentials{
		AccessToken:  "at",
		RefreshToken: "rt",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	})
	client, err := c.requireAuth(context.Background())
	if err != nil {
		t.Fatalf("requireAuth: %v", err)
	}
	if client == nil {
		t.Error("expected non-nil client")
	}
}

// TestRequireAuth_OAuthRefreshSucceeds exercises the refresh path. The
// test server returns a fresh token; requireAuth must persist it and
// build a client.
func TestRequireAuth_OAuthRefreshSucceeds(t *testing.T) {
	tEnv := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		if r.PostForm.Get("grant_type") != "refresh_token" {
			t.Errorf("expected refresh_token grant, got %q", r.PostForm.Get("grant_type"))
		}
		writeJSON(w, map[string]any{
			"access_token":  "new_at",
			"refresh_token": "new_rt",
			"expires_in":    3600,
			"token_type":    "Bearer",
		})
	}))

	// Seed an EXPIRED OAuth credential bundle so needsRefresh fires.
	creds := &credentials.Credentials{
		AccessToken:  "old_at",
		RefreshToken: "old_rt",
		ExpiresAt:    time.Now().Add(-1 * time.Hour),
	}
	if err := creds.Save(tEnv.CredentialsPath); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Reload through newCmdCtx so CredentialsPath matches the seeded file.
	c, err := newCmdCtx(false)
	if err != nil {
		t.Fatalf("newCmdCtx: %v", err)
	}
	var noticeBuf bytes.Buffer
	c = c.withRefreshNotice(&noticeBuf)

	client, err := c.requireAuth(context.Background())
	if err != nil {
		t.Fatalf("requireAuth: %v", err)
	}
	if client == nil {
		t.Error("expected non-nil client")
	}
	if c.Credentials.AccessToken != "new_at" {
		t.Errorf("expected access token rotated, got %q", c.Credentials.AccessToken)
	}
	// Refresh notice must have been emitted (non-JSON mode).
	if !strings.Contains(noticeBuf.String(), "Refreshed") {
		t.Errorf("expected refresh notice, got %q", noticeBuf.String())
	}
}

// TestRequireAuth_OAuthRefreshInvalidGrant: invalid_grant collapses to
// errNotAuthenticated so the user is prompted to log in again.
func TestRequireAuth_OAuthRefreshInvalidGrant(t *testing.T) {
	tEnv := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":             "invalid_grant",
			"error_description": "expired",
		})
	}))
	creds := &credentials.Credentials{
		AccessToken:  "old_at",
		RefreshToken: "dead_rt",
		ExpiresAt:    time.Now().Add(-1 * time.Hour),
	}
	if err := creds.Save(tEnv.CredentialsPath); err != nil {
		t.Fatalf("seed: %v", err)
	}

	c, err := newCmdCtx(false)
	if err != nil {
		t.Fatalf("newCmdCtx: %v", err)
	}
	if _, err := c.requireAuth(context.Background()); !errors.Is(err, errNotAuthenticated) {
		t.Errorf("expected errNotAuthenticated, got %v", err)
	}
}

// TestRefresh_PersistsAndNotifies hits c.refresh directly and confirms it
// updates the in-memory Credentials and notifies via refreshNoticeWriter.
func TestRefresh_PersistsAndNotifies(t *testing.T) {
	tEnv := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"access_token":  "rotated_at",
			"refresh_token": "rotated_rt",
			"expires_in":    3600,
			"token_type":    "Bearer",
		})
	}))
	creds := &credentials.Credentials{
		AccessToken:  "old",
		RefreshToken: "rt",
		ExpiresAt:    time.Now().Add(-1 * time.Hour),
	}
	if err := creds.Save(tEnv.CredentialsPath); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c, err := newCmdCtx(false)
	if err != nil {
		t.Fatalf("newCmdCtx: %v", err)
	}
	var notice bytes.Buffer
	c = c.withRefreshNotice(&notice)

	if err := c.refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if c.Credentials.AccessToken != "rotated_at" {
		t.Errorf("access token: got %q", c.Credentials.AccessToken)
	}
	if c.Credentials.RefreshToken != "rotated_rt" {
		t.Errorf("refresh token: got %q", c.Credentials.RefreshToken)
	}
	// Persistence check.
	reloaded, err := credentials.Load(c.CredentialsPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.AccessToken != "rotated_at" {
		t.Errorf("not persisted to disk, got %q", reloaded.AccessToken)
	}
	if !strings.Contains(notice.String(), "Refreshed") {
		t.Errorf("notice not written, got %q", notice.String())
	}
}

// TestWithRefreshNotice_JSONModeSuppressed asserts JSON-mode suppresses the
// refresh notice writer.
func TestWithRefreshNotice_JSONModeSuppressed(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())

	c := newCmdCtxForTest(t, &credentials.Credentials{})
	c.JSONMode = true
	var buf bytes.Buffer
	c = c.withRefreshNotice(&buf)
	if c.refreshNoticeWriter != nil {
		t.Error("expected refreshNoticeWriter to be nil in JSON mode")
	}
}

// TestDebugEnabled covers all branches: the --debug flag and the env var.
func TestDebugEnabled(t *testing.T) {
	prev := debugMode
	defer func() { debugMode = prev }()

	debugMode = false
	t.Setenv("EMAILABLE_DEBUG", "")
	if debugEnabled() {
		t.Error("expected false when neither flag nor env set")
	}
	debugMode = true
	if !debugEnabled() {
		t.Error("expected true when --debug set")
	}
	debugMode = false
	t.Setenv("EMAILABLE_DEBUG", "1")
	if !debugEnabled() {
		t.Error("expected true when EMAILABLE_DEBUG set")
	}
}

// TestClientOptions covers the debug-aware options factory.
func TestClientOptions(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())

	c := newCmdCtxForTest(t, &credentials.Credentials{})
	prev := debugMode
	defer func() { debugMode = prev }()

	debugMode = false
	if c.clientOptions().Debug {
		t.Error("expected Debug=false")
	}
	debugMode = true
	if !c.clientOptions().Debug {
		t.Error("expected Debug=true when debugMode is on")
	}
}

// Sanity check that the oauth.ErrInvalidGrant -> errNotAuthenticated wiring
// in requireAuth uses the canonical sentinel.
func TestOAuthInvalidGrantSentinel(t *testing.T) {
	if !errors.Is(oauth.ErrInvalidGrant, oauth.ErrInvalidGrant) {
		t.Fatal("sentinel self-equality")
	}
}
