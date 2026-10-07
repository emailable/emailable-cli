package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/emailable/emailable-cli/internal/credentials"
)

func TestAccountStatus_JSONPassesThroughUnknownFields(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"owner_email": "o@x.com", "available_credits": 5, "plan": "pro"})
	}))
	env.seedAPIKey(t, "sk_test_xxx")

	res := runRoot(t, "account", "status", "--json")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	if payload := decodeJSON(t, res.Stdout.Bytes()); payload["plan"] != "pro" {
		t.Errorf("expected unmodeled field to pass through, got %v", payload)
	}

	res = runRoot(t, "account", "status", "--jq", ".plan")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	if got := strings.TrimSpace(res.Stdout.String()); got != "pro" {
		t.Errorf("--jq .plan: got %q", got)
	}
}

func seedStoredKeyWithEmail(t *testing.T, env *testEnv) {
	t.Helper()
	creds := &credentials.Credentials{APIKey: "sk_test_xxx", OwnerEmail: "owner@example.com"}
	if err := creds.Save(env.CredentialsPath); err != nil {
		t.Fatal(err)
	}
}

func TestStatus_StoredAPIKeyShowsEmail(t *testing.T) {
	env := newTestEnv(t, http.NotFoundHandler())
	seedStoredKeyWithEmail(t, env)

	res := runRoot(t, "status", "--json")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	if payload := decodeJSON(t, res.Stdout.Bytes()); payload["owner_email"] != "owner@example.com" {
		t.Errorf("expected owner_email, got %v", payload)
	}

	res = runRoot(t, "status")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	if !strings.Contains(res.Stdout.String(), "owner@example.com") {
		t.Errorf("expected Account row, got %q", res.Stdout.String())
	}
}

func TestStatus_EnvAPIKeyHidesStoredEmail(t *testing.T) {
	env := newTestEnv(t, http.NotFoundHandler())
	seedStoredKeyWithEmail(t, env)
	t.Setenv("EMAILABLE_API_KEY", "sk_env_yyy")

	res := runRoot(t, "status", "--json")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	if payload := decodeJSON(t, res.Stdout.Bytes()); payload["owner_email"] != nil {
		t.Errorf("stored email must not show for an env key, got %v", payload["owner_email"])
	}
	res = runRoot(t, "status")
	if strings.Contains(res.Stdout.String(), "owner@example.com") {
		t.Errorf("stored email must not show for an env key, got %q", res.Stdout.String())
	}
}
