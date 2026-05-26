package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestVerify_Help asserts that the slim `verify` help surface carries no
// command-specific flags (only the inherited --json), and that none of the
// dropped --wait/--all/--field/--output flags appear.
func TestVerify_Help(t *testing.T) {
	root := newRootCmd("dev")
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"verify", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	for _, dropped := range []string{"--wait", "--all", "--field", "--output"} {
		if strings.Contains(got, dropped) {
			t.Errorf("expected help NOT to mention %s, got %q", dropped, got)
		}
	}
}

// TestVerify_RejectsCommaSeparated verifies a comma-joined string is
// rejected by the email-shape check (two @s → not a valid email).
func TestVerify_RejectsCommaSeparated(t *testing.T) {
	root := newRootCmd("dev")
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"verify", "a@x.com,b@y.com"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not a valid email") {
		t.Errorf("expected error to call out invalid email, got %q", err.Error())
	}
}

// TestVerify_RejectsBareWord makes sure non-email-shaped args (no @, no
// path separator, no extension) are rejected up front instead of being
// submitted to the API.
func TestVerify_RejectsBareWord(t *testing.T) {
	root := newRootCmd("dev")
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"verify", "hello"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not a valid email") {
		t.Errorf("expected invalid-email error, got %q", err.Error())
	}
}

// TestVerify_RejectsFilePath verifies the migration hint fires for a
// .csv-shaped argument too. We use a path that looks file-y but doesn't
// need to exist — looksLikeBatchInput is a pure-string heuristic.
func TestVerify_RejectsFilePath(t *testing.T) {
	root := newRootCmd("dev")
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"verify", "emails.csv"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "batch verify") {
		t.Errorf("expected error to mention 'batch verify', got %q", err.Error())
	}
}

// TestVerify_RejectsTooManyArgs ensures ExactArgs(1) is wired — a second
// positional argument should fail cobra arg validation, not be silently
// dropped.
func TestVerify_RejectsTooManyArgs(t *testing.T) {
	root := newRootCmd("dev")
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"verify", "a@x.com", "b@y.com"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error from ExactArgs(1)")
	}
}

// TestVerify_RejectsInvalidEmailUnderQuiet asserts that --quiet does NOT
// silence the validation error: errors keep printing under -q, matching the
// convention (curl/gh/docker all leak errors past --quiet).
func TestVerify_RejectsInvalidEmailUnderQuiet(t *testing.T) {
	root := newRootCmd("dev")
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"verify", "not-an-email", "--quiet"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for invalid email")
	}
	if !strings.Contains(err.Error(), "not a valid email") {
		t.Errorf("expected invalid-email error message, got %q", err.Error())
	}
}

// TestVerify_QuietSuppressesChrome runs a real verify against a stub server
// and asserts that --quiet collapses the human output: no Success/Hint/Notice
// chrome AND no spinner artifacts. The verify result itself still prints
// (Print, the structured renderer, isn't gated by Quiet — only the chrome
// methods are).
func TestVerify_QuietSuppressesChrome(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"email": "hello@example.com",
			"state": "deliverable",
			"score": 100,
		})
	}))
	env.seedAPIKey(t, "sk_test_xxx")

	res := runRoot(t, "verify", "hello@example.com", "--quiet")
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	// Stderr is where the spinner would have animated; must be empty.
	if got := res.Stderr.String(); strings.TrimSpace(got) != "" {
		t.Errorf("expected empty stderr under --quiet, got %q", got)
	}
	// Stdout should still carry the verify result (data, not chrome).
	if !strings.Contains(res.Stdout.String(), "hello@example.com") {
		t.Errorf("expected verify result in stdout, got %q", res.Stdout.String())
	}
}

// TestVerify_QuietJSON_StillEmitsJSON proves that --quiet does NOT affect
// --json output — the two flags are independent, and a script asking for
// machine output should always get its payload.
func TestVerify_QuietJSON_StillEmitsJSON(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"email": "hello@example.com",
			"state": "deliverable",
			"score": 100,
		})
	}))
	env.seedAPIKey(t, "sk_test_xxx")

	res := runRoot(t, "verify", "hello@example.com", "--quiet", "--json")
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(res.Stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON on stdout under --quiet --json: %v\nstdout: %s", err, res.Stdout.String())
	}
	if payload["email"] != "hello@example.com" {
		t.Errorf("expected email field in payload, got %v", payload)
	}
}

func TestBatchVerify_Help(t *testing.T) {
	root := newRootCmd("dev")
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"batch", "verify", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"--wait", "--all", "--field", "--output"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected batch verify help to mention %s, got %q", want, got)
		}
	}
}
