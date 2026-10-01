package cmd

import (
	"bytes"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"

	"github.com/emailable/emailable-cli/internal/credentials"
)

// TestOpenBrowser_Unsupported verifies the helper returns an error for an
// unknown GOOS. We can't usefully assert success on the current platform
// without actually launching a browser, so we only cover the negative case.
func TestOpenBrowser_Unsupported(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" ||
		runtime.GOOS == "windows" || runtime.GOOS == "freebsd" ||
		runtime.GOOS == "openbsd" || runtime.GOOS == "netbsd" {
		t.Skip("openBrowser supports this GOOS; nothing to assert for the unsupported branch")
	}
	if err := openBrowser("https://example.com"); err == nil {
		t.Errorf("expected error on unsupported platform %q, got nil", runtime.GOOS)
	}
}

// runRootWithStdin is runRoot with stdin wired to the supplied reader.
func runRootWithStdin(t *testing.T, stdin io.Reader, args ...string) *runResult {
	t.Helper()
	root := newRootCmd("test")
	var stdout, stderr bytes.Buffer
	root.SetIn(stdin)
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	err := root.Execute()
	return &runResult{Stdout: &stdout, Stderr: &stderr, Err: err}
}

// stubOpenBrowser keeps the OAuth tests from launching a real browser and
// records the URL the CLI tried to open.
func stubOpenBrowser(t *testing.T) *string {
	t.Helper()
	var opened string
	prev := openBrowser
	openBrowser = func(url string) error {
		opened = url
		return nil
	}
	t.Cleanup(func() { openBrowser = prev })
	return &opened
}

// oauthHandler serves a device flow that authorizes on the first poll.
func oauthHandler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/oauth/device/code":
		writeJSON(w, map[string]any{
			"device_code":               "dev_123",
			"user_code":                 "ABCD-EFGH",
			"verification_uri":          "https://example.com/device",
			"verification_uri_complete": "https://example.com/device?code=ABCD-EFGH&x=1",
			"expires_in":                600,
			"interval":                  1,
		})
	case "/oauth/token":
		writeJSON(w, map[string]any{
			"access_token":  "at_123",
			"refresh_token": "rt_123",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	default:
		accountHandler(w, r)
	}
}

func accountHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/account" {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, map[string]any{
		"owner_email":       "owner@example.com",
		"available_credits": 1,
	})
}

func TestLogin_APIKeyDash_ReadsStdin(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk_piped" {
			t.Errorf("expected Bearer sk_piped, got %q", got)
		}
		accountHandler(w, r)
	}))

	res := runRootWithStdin(t, strings.NewReader("  sk_piped\n"), "login", "--api-key", "-")
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	creds, err := credentials.Load(env.CredentialsPath)
	if err != nil {
		t.Fatalf("load credentials: %v", err)
	}
	if creds.APIKey != "sk_piped" {
		t.Errorf("APIKey: got %q want sk_piped", creds.APIKey)
	}
}

func TestLogin_APIKeyDash_EmptyStdin(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(accountHandler))

	res := runRootWithStdin(t, strings.NewReader(" \n"), "login", "--api-key", "-")
	if res.Err == nil {
		t.Fatal("expected error for empty stdin")
	}
	if got := errorCode(res.Err); got != codeInvalidInput {
		t.Errorf("errorCode: got %q want %q", got, codeInvalidInput)
	}
	creds, err := credentials.Load(env.CredentialsPath)
	if err != nil {
		t.Fatalf("load credentials: %v", err)
	}
	if creds.APIKey != "" {
		t.Errorf("APIKey should not be saved, got %q", creds.APIKey)
	}
}

func TestLogin_APIKeyDash_TerminalStdin(t *testing.T) {
	newTestEnv(t, http.HandlerFunc(accountHandler))
	prev := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return true }
	t.Cleanup(func() { stdinIsTerminal = prev })

	res := runRootWithStdin(t, strings.NewReader("sk_never_read"), "login", "--api-key", "-")
	if res.Err == nil {
		t.Fatal("expected error when stdin is a terminal")
	}
	if got := errorCode(res.Err); got != codeInvalidInput {
		t.Errorf("errorCode: got %q want %q", got, codeInvalidInput)
	}
}

// TestLogin_NoFlag_IgnoresStdin: without --api-key, login must not read
// stdin (a pipe that never closes would hang) and goes straight to OAuth.
func TestLogin_NoFlag_IgnoresStdin(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(oauthHandler))
	opened := stubOpenBrowser(t)

	stdin := bytes.NewBufferString("sk_should_not_be_read\n")
	res := runRootWithStdin(t, stdin, "login")
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	if stdin.Len() != len("sk_should_not_be_read\n") {
		t.Errorf("stdin was read: %d bytes remain", stdin.Len())
	}
	if *opened != "https://example.com/device?code=ABCD-EFGH&x=1" {
		t.Errorf("opened URL: got %q", *opened)
	}
	if !strings.Contains(res.Stderr.String(), "ABCD-EFGH") {
		t.Errorf("expected verification code on stderr, got %q", res.Stderr.String())
	}
	if !strings.Contains(res.Stdout.String(), "Logged in as owner@example.com") {
		t.Errorf("expected success message, got %q", res.Stdout.String())
	}

	creds, err := credentials.Load(env.CredentialsPath)
	if err != nil {
		t.Fatalf("load credentials: %v", err)
	}
	if creds.APIKey != "" || creds.AccessToken != "at_123" || creds.OwnerEmail != "owner@example.com" {
		t.Errorf("unexpected credentials: %+v", creds)
	}
}

func TestLogin_JSON_OAuth(t *testing.T) {
	newTestEnv(t, http.HandlerFunc(oauthHandler))
	stubOpenBrowser(t)

	res := runRootWithStdin(t, strings.NewReader(""), "login", "--json")
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	payload := decodeJSON(t, res.Stdout.Bytes())
	if payload["logged_in"] != true {
		t.Errorf("logged_in: got %v", payload["logged_in"])
	}
	if payload["auth_source"] != string(apiKeySourceOAuth) {
		t.Errorf("auth_source: got %v want %q", payload["auth_source"], apiKeySourceOAuth)
	}
	if payload["owner_email"] != "owner@example.com" {
		t.Errorf("owner_email: got %v", payload["owner_email"])
	}
	// The user still needs the code and URL, so they stay on stderr.
	if !strings.Contains(res.Stderr.String(), "ABCD-EFGH") {
		t.Errorf("expected verification code on stderr, got %q", res.Stderr.String())
	}
}

func TestLogin_JSON_OAuth_OmitsUnknownOwnerEmail(t *testing.T) {
	newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/account" {
			writeJSONError(w, http.StatusInternalServerError, "server_error", "boom")
			return
		}
		oauthHandler(w, r)
	}))
	stubOpenBrowser(t)

	res := runRootWithStdin(t, strings.NewReader(""), "login", "--json")
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	payload := decodeJSON(t, res.Stdout.Bytes())
	if payload["logged_in"] != true {
		t.Errorf("logged_in: got %v", payload["logged_in"])
	}
	if _, ok := payload["owner_email"]; ok {
		t.Errorf("owner_email should be omitted, got %v", payload["owner_email"])
	}
}

func TestLogin_JSON_APIKey(t *testing.T) {
	newTestEnv(t, http.HandlerFunc(accountHandler))

	res := runRoot(t, "login", "--api-key", "sk_json", "--json")
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	payload := decodeJSON(t, res.Stdout.Bytes())
	if payload["logged_in"] != true {
		t.Errorf("logged_in: got %v", payload["logged_in"])
	}
	if payload["auth_source"] != string(apiKeySourceStored) {
		t.Errorf("auth_source: got %v want %q", payload["auth_source"], apiKeySourceStored)
	}
	if payload["owner_email"] != "owner@example.com" {
		t.Errorf("owner_email: got %v", payload["owner_email"])
	}
}
