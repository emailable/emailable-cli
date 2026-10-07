package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emailable/emailable-cli/internal/api"
	"github.com/emailable/emailable-cli/internal/credentials"
)

func TestUserAgent_Format(t *testing.T) {
	re := regexp.MustCompile(`^emailable-cli/\S+ \([a-z0-9]+; [a-z0-9]+\)$`)
	if got := userAgent(); !re.MatchString(got) {
		t.Errorf("userAgent() = %q, want emailable-cli/<version> (<os>; <arch>)", got)
	}
}

func TestRequireAuth_SendsUserAgent(t *testing.T) {
	var got string
	tEnv := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		writeJSON(w, map[string]any{"owner_email": "a@b.com"})
	}))
	tEnv.seedAPIKey(t, "sk_test")

	res := runRoot(t, "account", "status", "--json")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	if got != userAgent() {
		t.Errorf("User-Agent: got %q, want %q", got, userAgent())
	}
}

// seedOAuth writes OAuth credentials and returns a cmdCtx loaded from them.
func seedOAuth(t *testing.T, tEnv *testEnv, creds *credentials.Credentials) *cmdCtx {
	t.Helper()
	if err := creds.Save(tEnv.CredentialsPath); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c, err := newCmdCtx(false)
	if err != nil {
		t.Fatalf("newCmdCtx: %v", err)
	}
	return c
}

func writeInvalidGrant(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
}

// TestRequireAuth_InvalidGrantAdoptsWinnersToken simulates losing a refresh
// race: our refresh token was rotated by a parallel invocation, which saved a
// fresh access token. We must adopt it instead of reporting "not logged in".
func TestRequireAuth_InvalidGrantAdoptsWinnersToken(t *testing.T) {
	var refreshes int32
	tEnv := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&refreshes, 1)
		writeInvalidGrant(w)
	}))
	c := seedOAuth(t, tEnv, &credentials.Credentials{
		AccessToken:  "old_at",
		RefreshToken: "old_rt",
		ExpiresAt:    time.Now().Add(-time.Hour),
	})
	// The winner saves its tokens after we loaded ours.
	winner := &credentials.Credentials{
		AccessToken:  "winner_at",
		RefreshToken: "winner_rt",
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	if err := winner.Save(tEnv.CredentialsPath); err != nil {
		t.Fatalf("save winner: %v", err)
	}

	if _, err := c.requireAuth(context.Background()); err != nil {
		t.Fatalf("requireAuth: %v", err)
	}
	if c.Credentials.AccessToken != "winner_at" {
		t.Errorf("expected winner's access token, got %q", c.Credentials.AccessToken)
	}
	if n := atomic.LoadInt32(&refreshes); n != 1 {
		t.Errorf("expected 1 refresh attempt, got %d", n)
	}
}

// TestRequireAuth_InvalidGrantCorruptFileReportsReadError: a credentials file
// that can't be parsed on reload surfaces its own error, not "not logged in".
func TestRequireAuth_InvalidGrantCorruptFileReportsReadError(t *testing.T) {
	tEnv := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeInvalidGrant(w)
	}))
	c := seedOAuth(t, tEnv, &credentials.Credentials{
		AccessToken:  "old_at",
		RefreshToken: "old_rt",
		ExpiresAt:    time.Now().Add(-time.Hour),
	})
	if err := os.WriteFile(tEnv.CredentialsPath, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupt credentials: %v", err)
	}

	_, err := c.requireAuth(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, errNotAuthenticated) {
		t.Errorf("expected the parse error, got errNotAuthenticated")
	}
	if !strings.Contains(err.Error(), "credentials: parse") {
		t.Errorf("expected a credentials parse error, got %v", err)
	}
}

// TestRequireAuth_InvalidGrantRefreshesWithSavedToken covers a saved refresh
// token that differs from ours but whose access token is already stale.
func TestRequireAuth_InvalidGrantRefreshesWithSavedToken(t *testing.T) {
	tEnv := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("refresh_token") != "winner_rt" {
			writeInvalidGrant(w)
			return
		}
		writeJSON(w, map[string]any{"access_token": "fresh_at", "refresh_token": "fresh_rt", "expires_in": 3600})
	}))
	c := seedOAuth(t, tEnv, &credentials.Credentials{
		AccessToken:  "old_at",
		RefreshToken: "old_rt",
		ExpiresAt:    time.Now().Add(-time.Hour),
	})
	winner := &credentials.Credentials{
		AccessToken:  "winner_at",
		RefreshToken: "winner_rt",
		ExpiresAt:    time.Now().Add(-time.Minute),
	}
	if err := winner.Save(tEnv.CredentialsPath); err != nil {
		t.Fatalf("save winner: %v", err)
	}

	if _, err := c.requireAuth(context.Background()); err != nil {
		t.Fatalf("requireAuth: %v", err)
	}
	if c.Credentials.AccessToken != "fresh_at" || c.Credentials.RefreshToken != "fresh_rt" {
		t.Errorf("expected refreshed tokens, got %+v", c.Credentials)
	}
	saved, err := credentials.Load(tEnv.CredentialsPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if saved.AccessToken != "fresh_at" {
		t.Errorf("expected refreshed token persisted, got %q", saved.AccessToken)
	}
}

// TestAPI401_RefreshesAndRetries: an access token rejected before its
// ExpiresAt gets one refresh, and the request is retried with the new token.
func TestAPI401_RefreshesAndRetries(t *testing.T) {
	var accountCalls int32
	tEnv := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			writeJSON(w, map[string]any{"access_token": "new_at", "refresh_token": "new_rt", "expires_in": 3600})
		case "/account":
			atomic.AddInt32(&accountCalls, 1)
			if r.Header.Get("Authorization") != "Bearer new_at" {
				writeJSONError(w, http.StatusUnauthorized, "", "Unauthorized")
				return
			}
			writeJSON(w, map[string]any{"owner_email": "a@b.com", "available_credits": 5})
		default:
			http.NotFound(w, r)
		}
	}))
	seedOAuth(t, tEnv, &credentials.Credentials{
		AccessToken:  "revoked_at",
		RefreshToken: "rt",
		ExpiresAt:    time.Now().Add(time.Hour),
	})

	res := runRoot(t, "account", "status", "--json")
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	if n := atomic.LoadInt32(&accountCalls); n != 2 {
		t.Errorf("expected 2 /account calls, got %d", n)
	}
	saved, err := credentials.Load(tEnv.CredentialsPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if saved.AccessToken != "new_at" {
		t.Errorf("expected refreshed token persisted, got %q", saved.AccessToken)
	}
}

// TestAPI401_RefreshInvalidGrantIsNotAuthenticated: when the refresh token is
// dead too (and nothing newer is on disk) the user must log in again.
func TestAPI401_RefreshInvalidGrantIsNotAuthenticated(t *testing.T) {
	tEnv := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			writeInvalidGrant(w)
			return
		}
		writeJSONError(w, http.StatusUnauthorized, "", "Unauthorized")
	}))
	seedOAuth(t, tEnv, &credentials.Credentials{
		AccessToken:  "revoked_at",
		RefreshToken: "dead_rt",
		ExpiresAt:    time.Now().Add(time.Hour),
	})

	res := runRoot(t, "account", "status")
	if !errors.Is(res.Err, errNotAuthenticated) {
		t.Fatalf("expected errNotAuthenticated, got %v", res.Err)
	}
}

func batchClient(t *testing.T, h http.HandlerFunc) *api.Client {
	t.Helper()
	newTestEnv(t, h)
	c := newCmdCtxForTest(t, &credentials.Credentials{APIKey: "sk_test"})
	client, err := c.requireAuth(context.Background())
	if err != nil {
		t.Fatalf("requireAuth: %v", err)
	}
	return client
}

func TestWaitForCompletion_RetriesTemporaryErrors(t *testing.T) {
	var calls int32
	client := batchClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		switch {
		case n <= 4:
			// Enough 503s to exhaust the client's own retries at least once.
			writeJSONError(w, http.StatusServiceUnavailable, "", "unavailable")
		case n == 5:
			writeJSONError(w, http.StatusTooManyRequests, "", "slow down")
		default:
			writeJSON(w, completedBatchPayload("bch_r"))
		}
	})

	s, err := waitForCompletion(context.Background(), client, "bch_r", true, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("waitForCompletion: %v", err)
	}
	if len(s.Emails) != 2 {
		t.Errorf("expected completed payload, got %+v", s)
	}
}

// TestWaitForCompletion_CanonicalFetchRetriesTemporaryErrors: when the counts
// say done but the follow-up fetch for the completed payload fails, --wait must
// keep trying instead of returning the count-only shape with no emails.
func TestWaitForCompletion_CanonicalFetchRetriesTemporaryErrors(t *testing.T) {
	var calls int32
	client := batchClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		switch {
		case n == 1:
			writeJSON(w, map[string]any{"id": "bch_c", "total": 2, "processed": 2})
		case n <= 4:
			writeJSONError(w, http.StatusServiceUnavailable, "", "unavailable")
		default:
			writeJSON(w, completedBatchPayload("bch_c"))
		}
	})

	s, err := waitForCompletion(context.Background(), client, "bch_c", true, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("waitForCompletion: %v", err)
	}
	if len(s.Emails) != 2 {
		t.Errorf("expected the completed payload with emails, got %+v", s)
	}
}

func TestWaitForCompletion_GivesUpAfterConsecutiveFailures(t *testing.T) {
	var calls int32
	client := batchClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		writeJSONError(w, http.StatusBadGateway, "", "bad gateway")
	})

	_, err := waitForCompletion(context.Background(), client, "bch_x", true, &bytes.Buffer{})
	if errorCode(err) != codeServerError {
		t.Fatalf("expected server_error, got %v (%s)", err, errorCode(err))
	}
	// Each poll is one client call with its own retries.
	if want := int32(maxPollFailures * 3); atomic.LoadInt32(&calls) != want {
		t.Errorf("expected %d requests, got %d", want, calls)
	}
}

func TestWaitForCompletion_FailureCountResetsOnSuccess(t *testing.T) {
	var calls int32
	client := batchClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		// Alternate long failure runs (each one poll short of giving up) with
		// a verifying response, then complete.
		const run = (maxPollFailures - 1) * 3
		switch {
		case n <= run, n > run+1 && n <= 2*run+1:
			writeJSONError(w, http.StatusBadGateway, "", "bad gateway")
		case n == run+1:
			writeJSON(w, map[string]any{"id": "bch_x", "total": 2, "processed": 1, "status": "verifying"})
		default:
			writeJSON(w, completedBatchPayload("bch_x"))
		}
	})

	if _, err := waitForCompletion(context.Background(), client, "bch_x", true, &bytes.Buffer{}); err != nil {
		t.Fatalf("waitForCompletion: %v", err)
	}
}

func TestWaitForCompletion_NonTemporaryErrorReturnsImmediately(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusBadRequest} {
		var calls int32
		client := batchClient(t, func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			writeJSONError(w, status, "", "nope")
		})

		_, err := waitForCompletion(context.Background(), client, "bch_x", true, &bytes.Buffer{})
		var apiErr *api.Error
		if !errors.As(err, &apiErr) || apiErr.StatusCode != status {
			t.Errorf("%d: expected matching *api.Error, got %v", status, err)
		}
		if n := atomic.LoadInt32(&calls); n != 1 {
			t.Errorf("%d: expected 1 request, got %d", status, n)
		}
	}
}

func TestWaitForCompletion_CanceledContextReturnsImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls int32
	client := batchClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		cancel()
		writeJSON(w, map[string]any{"id": "bch_x", "total": 2, "processed": 0, "status": "verifying"})
	})

	_, err := waitForCompletion(ctx, client, "bch_x", true, &bytes.Buffer{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("expected 1 request, got %d", n)
	}
	if exitCode(err) != exitInterrupted {
		t.Errorf("exit code: got %d, want %d", exitCode(err), exitInterrupted)
	}
}

// TestBatchVerifyWait_FailureCarriesBatchID: credits are spent once the batch
// is submitted, so a failed --wait must still report the id in JSON mode.
func TestBatchVerifyWait_FailureCarriesBatchID(t *testing.T) {
	var submits int32
	tEnv := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			atomic.AddInt32(&submits, 1)
			writeJSON(w, map[string]any{"id": "bch_lost", "message": "queued"})
			return
		}
		writeJSONError(w, http.StatusNotFound, "not_found", "Batch not found")
	}))
	tEnv.seedAPIKey(t, "sk_test")

	res := runRoot(t, "batch", "verify", "a@example.com", "--wait", "--json")
	if res.Err == nil {
		t.Fatal("expected error")
	}
	if exitCode(res.Err) != exitInput {
		t.Errorf("wrapping must keep classification: exit %d, want %d", exitCode(res.Err), exitInput)
	}
	if n := atomic.LoadInt32(&submits); n != 1 {
		t.Errorf("expected 1 submit, got %d", n)
	}

	var buf bytes.Buffer
	renderError(&buf, res.Err, true)
	payload := decodeJSON(t, buf.Bytes())
	if payload["batch_id"] != "bch_lost" {
		t.Errorf("expected batch_id in JSON error, got %v", payload)
	}
	if payload["code"] != codeNotFound {
		t.Errorf("expected code not_found, got %v", payload["code"])
	}
}

func TestRenderError_BatchWaitError(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"api_object_body", &api.Error{StatusCode: 503, Body: []byte(`{"message":"down"}`)}},
		{"api_non_json_body", &api.Error{StatusCode: 502, Body: []byte("<html>")}},
		{"non_api", fmt.Errorf("http: %w", errors.New("connection refused"))},
		{"interrupted", context.Canceled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := &batchWaitError{ID: "bch_1", Err: tc.err}

			var jsonBuf bytes.Buffer
			renderError(&jsonBuf, err, true)
			if got := decodeJSON(t, jsonBuf.Bytes())["batch_id"]; got != "bch_1" {
				t.Errorf("JSON batch_id: got %v in %s", got, jsonBuf.String())
			}

			var humanBuf bytes.Buffer
			renderError(&humanBuf, err, false)
			if !strings.Contains(humanBuf.String(), "emailable batch get bch_1 --wait") {
				t.Errorf("human output missing resume hint: %q", humanBuf.String())
			}
		})
	}
}

func TestRenderError_Interrupted(t *testing.T) {
	err := fmt.Errorf("http: %w", context.Canceled)
	if got := errorCode(err); got != codeInterrupted {
		t.Errorf("errorCode: got %q, want %q", got, codeInterrupted)
	}
	if got := exitCode(err); got != 130 {
		t.Errorf("exitCode: got %d, want 130", got)
	}

	var human bytes.Buffer
	renderError(&human, err, false)
	if human.String() != "Error: interrupted\n" {
		t.Errorf("human: got %q", human.String())
	}
	var js bytes.Buffer
	renderError(&js, err, true)
	payload := decodeJSON(t, js.Bytes())
	if payload["code"] != codeInterrupted || payload["message"] != "interrupted" {
		t.Errorf("json: got %v", payload)
	}
}

// TestBodyReadFailure_IsNetworkError: a connection dropped after the headers
// (short body) must classify as network, or `--wait` would treat it as final.
func TestBodyReadFailure_IsNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte(`{"id":`))
	}))
	defer srv.Close()

	c := api.NewWithOptions(srv.URL, "tok", api.Options{MaxRetries: -1})
	_, err := c.Batch(context.Background(), "bch_1", false)
	if err == nil {
		t.Fatal("expected a body read error")
	}
	if got := errorCode(err); got != codeNetwork {
		t.Errorf("errorCode: got %q want %q (err: %v)", got, codeNetwork, err)
	}
	if !isTemporaryPollError(err) {
		t.Error("expected a mid-body drop to be a temporary poll error")
	}
}
