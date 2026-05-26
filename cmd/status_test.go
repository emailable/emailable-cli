package cmd

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/emailable/emailable-cli/internal/credentials"
)

// TestStatus_NotLoggedIn covers the empty-config human path and exercises
// printStatusHuman's "Not logged in" branch.
func TestStatus_NotLoggedIn(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())

	res := runRoot(t, "status")
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	out := res.Stdout.String()
	if !strings.Contains(out, "Not logged in") {
		t.Errorf("expected 'Not logged in' in output, got %q", out)
	}
	if !strings.Contains(out, "none") {
		t.Errorf("expected source 'none', got %q", out)
	}
}

// TestStatus_NotLoggedIn_JSON covers the JSON path with logged_in:false.
func TestStatus_NotLoggedIn_JSON(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())

	res := runRoot(t, "status", "--json")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	payload := decodeJSON(t, res.Stdout.Bytes())
	if payload["logged_in"].(bool) != false {
		t.Errorf("expected logged_in=false, got %v", payload)
	}
	if payload["auth_source"] != "none" {
		t.Errorf("expected auth_source=none, got %v", payload)
	}
}

// TestStatus_APIKeyStored exercises the apiKeySourceStored branch.
func TestStatus_APIKeyStored(t *testing.T) {
	env := newTestEnv(t, http.NotFoundHandler())
	env.seedAPIKey(t, "sk_test_xxx")

	res := runRoot(t, "status", "--json")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	payload := decodeJSON(t, res.Stdout.Bytes())
	if payload["logged_in"].(bool) != true {
		t.Errorf("expected logged_in=true, got %v", payload)
	}
	if payload["auth_source"] != string(apiKeySourceStored) {
		t.Errorf("expected stored api-key source, got %v", payload)
	}
}

// TestStatus_APIKeyEnv exercises the env-var branch (EMAILABLE_API_KEY set).
func TestStatus_APIKeyEnv(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())
	t.Setenv("EMAILABLE_API_KEY", "sk_env_yyy")

	res := runRoot(t, "status", "--json")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	payload := decodeJSON(t, res.Stdout.Bytes())
	if payload["auth_source"] != string(apiKeySourceEnv) {
		t.Errorf("expected env api-key source, got %v", payload)
	}
}

// TestStatus_OAuth exercises the oauth branch with an expiry, including
// expires_at/expires_in and the human "Account" row.
func TestStatus_OAuth(t *testing.T) {
	env := newTestEnv(t, http.NotFoundHandler())
	creds := &credentials.Credentials{
		AccessToken:  "at_xxx",
		RefreshToken: "rt_xxx",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
		OwnerEmail:   "owner@example.com",
	}
	if err := creds.Save(env.CredentialsPath); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Human path first.
	res := runRoot(t, "status")
	if res.Err != nil {
		t.Fatalf("human execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	out := res.Stdout.String()
	if !strings.Contains(out, "Logged in") {
		t.Errorf("expected 'Logged in' in output, got %q", out)
	}
	if !strings.Contains(out, "owner@example.com") {
		t.Errorf("expected owner_email in human output, got %q", out)
	}

	// Now JSON path with the same config.
	res2 := runRoot(t, "status", "--json")
	if res2.Err != nil {
		t.Fatalf("json execute: %v", res2.Err)
	}
	payload := decodeJSON(t, res2.Stdout.Bytes())
	if payload["auth_source"] != string(apiKeySourceOAuth) {
		t.Errorf("expected oauth source, got %v", payload)
	}
	if payload["owner_email"] != "owner@example.com" {
		t.Errorf("expected owner_email in JSON, got %v", payload)
	}
	if _, ok := payload["expires_at"]; !ok {
		t.Errorf("expected expires_at in JSON, got %v", payload)
	}
}

// TestStatus_OAuthExpired exercises the "expired" branch of the human
// renderer (expires_in <= 0).
func TestStatus_OAuthExpired(t *testing.T) {
	env := newTestEnv(t, http.NotFoundHandler())
	creds := &credentials.Credentials{
		AccessToken:  "at_xxx",
		RefreshToken: "rt_xxx",
		ExpiresAt:    time.Now().Add(-1 * time.Hour),
	}
	if err := creds.Save(env.CredentialsPath); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res := runRoot(t, "status")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	if !strings.Contains(res.Stdout.String(), "expired") {
		t.Errorf("expected 'expired' marker, got %q", res.Stdout.String())
	}
}

func TestHumanizeSeconds(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{30, "30s"},
		{120, "2m"},
		{3600 * 2, "2h"},
		{86400 * 3, "3d"},
	}
	for _, tc := range cases {
		if got := humanizeSeconds(tc.in); got != tc.want {
			t.Errorf("humanizeSeconds(%d): got %q want %q", tc.in, got, tc.want)
		}
	}
}
