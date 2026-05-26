package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestVerify_200(t *testing.T) {
	var gotURL string
	var gotAuth string
	var gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"email":"foo@bar.com","state":"deliverable","score":100}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok123", nil)
	result, err := c.Verify(context.Background(), "foo@bar.com", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Email != "foo@bar.com" || result.State != "deliverable" {
		t.Errorf("unexpected result: %+v", result)
	}
	if !strings.HasPrefix(gotURL, "/verify?") || !strings.Contains(gotURL, "email=foo%40bar.com") {
		t.Errorf("unexpected URL: %s", gotURL)
	}
	if gotAuth != "Bearer tok123" {
		t.Errorf("unexpected Authorization header: %s", gotAuth)
	}
	if gotAccept != "application/json" {
		t.Errorf("unexpected Accept header: %s", gotAccept)
	}
}

func TestVerify_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message":"Unauthorized"}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "bad", nil)
	_, err := c.Verify(context.Background(), "foo@bar.com", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("expected ErrUnauthenticated, got %v", err)
	}
}

func TestVerify_422(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		_, _ = io.WriteString(w, `{"message":"Invalid email"}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", nil)
	_, err := c.Verify(context.Background(), "bad", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 422 {
		t.Errorf("expected StatusCode 422, got %d", apiErr.StatusCode)
	}
	if apiErr.Message != "Invalid email" {
		t.Errorf("expected Message 'Invalid email', got %q", apiErr.Message)
	}
}

// TestVerify_429_RateLimit asserts that the IETF-draft `RateLimit-*` headers
// are captured into *Error.RateLimit on a 429 response so callers (the
// renderer in cmd/errors.go) can surface a retry hint. Retries are disabled
// here so the test doesn't actually sleep for the advertised window.
func TestVerify_429_RateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("RateLimit-Limit", "1000")
		w.Header().Set("RateLimit-Remaining", "0")
		w.Header().Set("RateLimit-Reset", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"message":"Too Many Requests"}`)
	}))
	defer srv.Close()

	c := NewWithOptions(srv.URL, "tok", Options{MaxRetries: -1})
	_, err := c.Verify(context.Background(), "foo@bar.com", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 429 {
		t.Errorf("expected StatusCode 429, got %d", apiErr.StatusCode)
	}
	if apiErr.RateLimit == nil {
		t.Fatal("expected RateLimit to be populated")
	}
	if apiErr.RateLimit.Limit != 1000 {
		t.Errorf("expected Limit 1000, got %d", apiErr.RateLimit.Limit)
	}
	if apiErr.RateLimit.Remaining != 0 {
		t.Errorf("expected Remaining 0, got %d", apiErr.RateLimit.Remaining)
	}
	if apiErr.RateLimit.Reset != 60 {
		t.Errorf("expected Reset 60, got %d", apiErr.RateLimit.Reset)
	}
}

// TestVerify_NoRateLimitHeaders confirms RateLimit stays nil when the API
// response carries none of the draft headers (the common non-429 case).
func TestVerify_NoRateLimitHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = io.WriteString(w, `{"message":"boom"}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", nil)
	_, err := c.Verify(context.Background(), "foo@bar.com", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *Error, got %T", err)
	}
	if apiErr.RateLimit != nil {
		t.Errorf("expected RateLimit to be nil, got %+v", apiErr.RateLimit)
	}
}

// TestVerify_RateLimitHeaders_PartialAndMalformed checks the leniency
// promised by parseRateLimit: a single header is enough to allocate the
// struct, and unparseable values silently fall back to zero rather than
// breaking the error path.
func TestVerify_RateLimitHeaders_PartialAndMalformed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("RateLimit-Limit", "1000")
		w.Header().Set("RateLimit-Reset", "not-a-number")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"message":"rate"}`)
	}))
	defer srv.Close()

	c := NewWithOptions(srv.URL, "tok", Options{MaxRetries: -1})
	_, err := c.Verify(context.Background(), "foo@bar.com", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *Error, got %T", err)
	}
	if apiErr.RateLimit == nil {
		t.Fatal("expected RateLimit to be populated when any header is present")
	}
	if apiErr.RateLimit.Limit != 1000 {
		t.Errorf("expected Limit 1000, got %d", apiErr.RateLimit.Limit)
	}
	if apiErr.RateLimit.Reset != 0 {
		t.Errorf("expected unparseable Reset to fall back to 0, got %d", apiErr.RateLimit.Reset)
	}
}

// TestVerify_429_RetriesUntilSuccess asserts the client retries a 429
// automatically, honoring RateLimit-Reset for the backoff (clamped to the
// floor so a 0-second reset still pauses briefly).
func TestVerify_429_RetriesUntilSuccess(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("RateLimit-Reset", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"message":"rate"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"email":"a@b.com","state":"deliverable","score":100}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", nil)
	result, err := c.Verify(context.Background(), "a@b.com", nil)
	if err != nil {
		t.Fatalf("unexpected error after retry: %v", err)
	}
	if result.State != "deliverable" {
		t.Errorf("expected deliverable on retried response, got %q", result.State)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls (1 retry), got %d", calls)
	}
}

// TestDebug_DumpsAndRedacts asserts that Debug=true causes the request and
// response to be dumped to DebugOut, with the Authorization header redacted
// so the bearer token never leaks into logs an agent might forward.
func TestDebug_DumpsAndRedacts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"email":"a@b.com","state":"deliverable","score":100}`)
	}))
	defer srv.Close()

	var buf strings.Builder
	c := NewWithOptions(srv.URL, "supersecret", Options{Debug: true, DebugOut: &buf})
	if _, err := c.Verify(context.Background(), "a@b.com", nil); err != nil {
		t.Fatalf("verify: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "DEBUG ==> outgoing request") {
		t.Errorf("expected request banner in debug output, got:\n%s", out)
	}
	if !strings.Contains(out, "DEBUG <== incoming response") {
		t.Errorf("expected response banner in debug output, got:\n%s", out)
	}
	if strings.Contains(out, "supersecret") {
		t.Errorf("expected Authorization to be redacted, but token leaked into:\n%s", out)
	}
	if !strings.Contains(out, "Bearer [redacted]") {
		t.Errorf("expected redacted bearer placeholder in debug output, got:\n%s", out)
	}
}

// TestVerify_429_GivesUpAfterMaxRetries asserts the client surfaces the 429
// error once the retry budget is exhausted instead of looping forever.
func TestVerify_429_GivesUpAfterMaxRetries(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("RateLimit-Reset", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"message":"rate"}`)
	}))
	defer srv.Close()

	c := NewWithOptions(srv.URL, "tok", Options{MaxRetries: 1})
	_, err := c.Verify(context.Background(), "a@b.com", nil)
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 429 {
		t.Errorf("expected 429 *Error, got %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls (initial + 1 retry), got %d", calls)
	}
}

func TestSubmitBatch_POSTBody(t *testing.T) {
	var gotContentType string
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"abc","message":"queued"}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", nil)
	submit, err := c.SubmitBatch(context.Background(), []string{"a@x.com", "b@y.com"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if submit.ID != "abc" {
		t.Errorf("unexpected id: %s", submit.ID)
	}
	if gotContentType != "application/x-www-form-urlencoded" {
		t.Errorf("unexpected Content-Type: %s", gotContentType)
	}
	form, err := url.ParseQuery(gotBody)
	if err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if form.Get("emails") != "a@x.com,b@y.com" {
		t.Errorf("unexpected emails: %s", form.Get("emails"))
	}
}
