package cmd

import (
	"net/http"
	"strings"
	"testing"
)

// TestAccountStatus_HappyPath exercises the GET /v1/account happy path.
func TestAccountStatus_HappyPath(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/account" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, map[string]any{
			"owner_email":       "owner@example.com",
			"available_credits": 4242,
		})
	}))
	env.seedAPIKey(t, "sk_test_xxx")

	res := runRoot(t, "account", "status")
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	out := res.Stdout.String()
	if !strings.Contains(out, "owner@example.com") {
		t.Errorf("expected owner email in output, got %q", out)
	}
	// formatThousands inserts a comma; assert the thousands-separated form.
	if !strings.Contains(out, "4,242") {
		t.Errorf("expected credits in output, got %q", out)
	}
}

// TestAccountStatus_JSON checks --json passes through the account body.
func TestAccountStatus_JSON(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"owner_email":       "owner@example.com",
			"available_credits": 99,
		})
	}))
	env.seedAPIKey(t, "sk_test_xxx")

	res := runRoot(t, "account", "status", "--json")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	payload := decodeJSON(t, res.Stdout.Bytes())
	if payload["owner_email"] != "owner@example.com" {
		t.Errorf("owner_email: got %v", payload["owner_email"])
	}
	// JSON numbers decode to float64.
	if payload["available_credits"].(float64) != 99 {
		t.Errorf("available_credits: got %v", payload["available_credits"])
	}
}

// TestAccountStatus_401 maps a 401 from /v1/account to exit code 2 /
// not_authenticated.
func TestAccountStatus_401(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "bad token")
	}))
	env.seedAPIKey(t, "sk_test_xxx")

	res := runRoot(t, "account", "status")
	if res.Err == nil {
		t.Fatal("expected error from 401")
	}
	if got := errorCode(res.Err); got != codeNotAuthenticated {
		t.Errorf("errorCode: got %q want %q", got, codeNotAuthenticated)
	}
	if got := exitCode(res.Err); got != exitAuth {
		t.Errorf("exitCode: got %d want %d", got, exitAuth)
	}
}
