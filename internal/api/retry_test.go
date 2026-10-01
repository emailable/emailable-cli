package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func noSleep(context.Context, time.Duration) error { return nil }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUserAgentHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	c := NewWithOptions(srv.URL, "tok", Options{UserAgent: "emailable-cli/1.2.3 (darwin; arm64)"})
	if _, err := c.Account(context.Background()); err != nil {
		t.Fatalf("account: %v", err)
	}
	if got != "emailable-cli/1.2.3 (darwin; arm64)" {
		t.Errorf("User-Agent: got %q", got)
	}
}

// TestGet_5xxRetriesUntilSuccess covers every retried 5xx status on a GET.
func TestGet_5xxRetriesUntilSuccess(t *testing.T) {
	for _, status := range []int{500, 502, 503, 504} {
		var calls int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				w.WriteHeader(status)
				return
			}
			_, _ = io.WriteString(w, `{"email":"a@b.com","state":"deliverable"}`)
		}))

		c := NewWithOptions(srv.URL, "tok", Options{Sleep: noSleep})
		res, err := c.Verify(context.Background(), "a@b.com", nil)
		srv.Close()
		if err != nil {
			t.Fatalf("%d: unexpected error: %v", status, err)
		}
		if res.State != "deliverable" || calls != 2 {
			t.Errorf("%d: got state %q after %d calls, want deliverable after 2", status, res.State, calls)
		}
	}
}

func TestGet_501NotRetried(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotImplemented)
	}))
	defer srv.Close()

	c := NewWithOptions(srv.URL, "tok", Options{Sleep: noSleep})
	if _, err := c.Account(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestGet_TransportErrorRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"owner_email":"a@b.com"}`)
	}))
	defer srv.Close()

	var calls int
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("connection reset by peer")
		}
		return http.DefaultTransport.RoundTrip(r)
	})
	c := NewWithOptions(srv.URL, "tok", Options{HTTPClient: &http.Client{Transport: rt}, Sleep: noSleep})
	if _, err := c.Account(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
}

func TestGet_TransportErrorGivesUp(t *testing.T) {
	var calls int
	rt := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("connection refused")
	})
	c := NewWithOptions("http://example.invalid", "tok", Options{HTTPClient: &http.Client{Transport: rt}, Sleep: noSleep})
	_, err := c.Account(context.Background())
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("expected transport error, got %v", err)
	}
	if calls != defaultMaxRetries+1 {
		t.Errorf("expected %d calls, got %d", defaultMaxRetries+1, calls)
	}
}

// TestGet_CanceledContextNotRetried: a canceled ctx is the caller giving up,
// not a flaky network, so it must return after the first attempt.
func TestGet_CanceledContextNotRetried(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls int
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		cancel()
		return nil, r.Context().Err()
	})
	c := NewWithOptions("http://example.invalid", "tok", Options{HTTPClient: &http.Client{Transport: rt}, Sleep: noSleep})
	_, err := c.Account(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

// TestSubmitBatch_5xxAndTransportNotRetried guards against double-submitting a
// batch: only 249/429 (where the server did no work) may retry a POST.
func TestSubmitBatch_5xxAndTransportNotRetried(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := NewWithOptions(srv.URL, "tok", Options{Sleep: noSleep})
	if _, err := c.SubmitBatch(context.Background(), []string{"a@b.com"}, nil); err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("502: expected 1 call, got %d", calls)
	}

	calls = 0
	rt := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("connection reset by peer")
	})
	c = NewWithOptions(srv.URL, "tok", Options{HTTPClient: &http.Client{Transport: rt}, Sleep: noSleep})
	if _, err := c.SubmitBatch(context.Background(), []string{"a@b.com"}, nil); err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("transport error: expected 1 call, got %d", calls)
	}
}

func TestSubmitBatch_429StillRetries(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, `{"id":"bch_1"}`)
	}))
	defer srv.Close()

	c := NewWithOptions(srv.URL, "tok", Options{Sleep: noSleep})
	sub, err := c.SubmitBatch(context.Background(), []string{"a@b.com"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sub.ID != "bch_1" || calls != 2 {
		t.Errorf("got id %q after %d calls", sub.ID, calls)
	}
}

func TestOnUnauthorized_RetriesWithNewToken(t *testing.T) {
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") != "Bearer new" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"id":"bch_1"}`)
	}))
	defer srv.Close()

	var hookCalls int
	c := NewWithOptions(srv.URL, "old", Options{
		Sleep: noSleep,
		OnUnauthorized: func(context.Context) (string, error) {
			hookCalls++
			return "new", nil
		},
	})
	// A POST: the 401 retry is safe for every method.
	if _, err := c.SubmitBatch(context.Background(), []string{"a@b.com"}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hookCalls != 1 {
		t.Errorf("expected 1 hook call, got %d", hookCalls)
	}
	if len(auths) != 2 || auths[0] != "Bearer old" || auths[1] != "Bearer new" {
		t.Fatalf("unexpected Authorization sequence: %v", auths)
	}
	// Later requests keep using the refreshed token.
	if _, err := c.Batch(context.Background(), "bch_1", false); err != nil {
		t.Fatalf("follow-up request: %v", err)
	}
	if hookCalls != 1 || auths[2] != "Bearer new" {
		t.Errorf("follow-up: hook calls %d, auth %q", hookCalls, auths[2])
	}
}

func TestOnUnauthorized_AtMostOncePerRequest(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	var hookCalls int
	c := NewWithOptions(srv.URL, "old", Options{
		Sleep: noSleep,
		OnUnauthorized: func(context.Context) (string, error) {
			hookCalls++
			return "still-bad", nil
		},
	})
	_, err := c.Account(context.Background())
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expected ErrUnauthenticated, got %v", err)
	}
	if hookCalls != 1 || calls != 2 {
		t.Errorf("expected 1 hook call and 2 requests, got %d and %d", hookCalls, calls)
	}
}

func TestOnUnauthorized_HookErrorReturned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	hookErr := errors.New("refresh failed")
	c := NewWithOptions(srv.URL, "old", Options{
		OnUnauthorized: func(context.Context) (string, error) { return "", hookErr },
	})
	if _, err := c.Account(context.Background()); !errors.Is(err, hookErr) {
		t.Fatalf("expected hook error, got %v", err)
	}
}
