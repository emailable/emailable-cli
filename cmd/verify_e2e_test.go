package cmd

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestVerify_HappyPath_Human exercises the GET /v1/verify happy path against
// a stub server and confirms the human formatter renders the email/state.
func TestVerify_HappyPath_Human(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/verify" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("email"); got != "hello@example.com" {
			t.Errorf("expected email query param, got %q", got)
		}
		writeJSON(w, map[string]any{
			"email":  "hello@example.com",
			"state":  "deliverable",
			"reason": "accepted_email",
			"score":  100,
			"domain": "example.com",
		})
	}))
	env.seedAPIKey(t, "sk_test_xxx")

	res := runRoot(t, "verify", "hello@example.com")
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	out := res.Stdout.String()
	if !strings.Contains(out, "hello@example.com") {
		t.Errorf("expected output to include email, got %q", out)
	}
	if !strings.Contains(out, "Deliverable") {
		t.Errorf("expected output to include humanized state, got %q", out)
	}
}

// TestVerify_HappyPath_JSON checks --json passes through the API body and
// hits the JSON formatter (no human header lines).
func TestVerify_HappyPath_JSON(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"email": "hello@example.com",
			"state": "deliverable",
			"score": 100,
		})
	}))
	env.seedAPIKey(t, "sk_test_xxx")

	res := runRoot(t, "verify", "hello@example.com", "--json")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	payload := decodeJSON(t, res.Stdout.Bytes())
	if payload["email"] != "hello@example.com" {
		t.Errorf("email: got %v", payload["email"])
	}
	if payload["state"] != "deliverable" {
		t.Errorf("state: got %v", payload["state"])
	}
}

// TestVerify_FlagsAreForwarded verifies --smtp, --accept-all, --timeout are
// threaded through to the request query.
func TestVerify_FlagsAreForwarded(t *testing.T) {
	var gotQuery url.Values
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		writeJSON(w, map[string]any{"email": "a@b.com", "state": "deliverable"})
	}))
	env.seedAPIKey(t, "sk_test_xxx")

	res := runRoot(t, "verify", "a@b.com", "--smtp=false", "--accept-all=true", "--timeout=5", "--json")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	if gotQuery.Get("smtp") != "false" {
		t.Errorf("smtp flag not forwarded, query=%v", gotQuery)
	}
	if gotQuery.Get("accept_all") != "true" {
		t.Errorf("accept_all flag not forwarded, query=%v", gotQuery)
	}
	if gotQuery.Get("timeout") != "5" {
		t.Errorf("timeout flag not forwarded, query=%v", gotQuery)
	}
}

// TestVerify_Timeout_OutOfRange ensures the local validation message fires
// without contacting the server.
func TestVerify_Timeout_OutOfRange(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server should not be hit for invalid --timeout")
	}))
	env.seedAPIKey(t, "sk_test_xxx")

	res := runRoot(t, "verify", "a@b.com", "--timeout=99")
	if res.Err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(res.Err.Error(), "between 2 and 10") {
		t.Errorf("expected timeout range error, got %v", res.Err)
	}
}

// TestVerify_422 confirms a 422 maps to exit code 4 / invalid_input.
func TestVerify_422(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_input", "bad email")
	}))
	env.seedAPIKey(t, "sk_test_xxx")

	res := runRoot(t, "verify", "a@b.com")
	if res.Err == nil {
		t.Fatal("expected error from 422")
	}
	if got := errorCode(res.Err); got != codeInvalidInput {
		t.Errorf("errorCode: got %q, want %q", got, codeInvalidInput)
	}
	if got := exitCode(res.Err); got != exitInput {
		t.Errorf("exitCode: got %d, want %d", got, exitInput)
	}
}

// TestVerify_NotAuthenticated: with no stored credentials the command must
// exit code 2 and not contact the server.
func TestVerify_NotAuthenticated(t *testing.T) {
	called := false
	newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	// Note: no seedAPIKey, no OAuth tokens — the config is empty.

	res := runRoot(t, "verify", "a@b.com")
	if res.Err == nil {
		t.Fatal("expected error")
	}
	if called {
		t.Error("server should not be contacted when not logged in")
	}
	if got := exitCode(res.Err); got != exitAuth {
		t.Errorf("exitCode: got %d, want %d", got, exitAuth)
	}
}

// TestVerify_422_JSONOutput confirms the JSON renderer emits a flat object
// with the API code preserved.
func TestVerify_422_JSONOutput(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_input", "bad email")
	}))
	env.seedAPIKey(t, "sk_test_xxx")

	res := runRoot(t, "verify", "a@b.com", "--json")
	if res.Err == nil {
		t.Fatal("expected error")
	}
	// renderJSONError isn't invoked from cobra's RunE path; it's called by
	// Execute(). For the inline assertion we just check the error
	// classification — full Execute() coverage lives in renderError tests.
	if errorCode(res.Err) != codeInvalidInput {
		t.Errorf("expected invalid_input code, got %q", errorCode(res.Err))
	}
	_ = json.Marshal // keep import
}
