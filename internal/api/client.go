// Package api is the HTTP client for the Emailable v1 REST API. All requests
// carry `Authorization: Bearer <accessToken>`.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// defaultRequestTimeout caps the total wall time for a single API call.
// Generous because a real-time verify can spend ~30s SMTP-probing slow MX
// hosts, but bounded so a hung connection can't wedge the CLI forever.
const defaultRequestTimeout = 60 * time.Second

// Retry knobs for 429 handling. maxRetrySleep caps the per-attempt wait so a
// misbehaving server can't wedge us for hours; minRetrySleep ensures a brief
// pause even when the server returned an unparseable / zero Reset.
const (
	defaultMaxRetries = 2
	maxRetrySleep     = 60 * time.Second
	minRetrySleep     = 500 * time.Millisecond
)

// Options tunes a Client. All fields are optional.
type Options struct {
	// HTTPClient is the underlying transport. nil => a private client with a
	// bounded per-request timeout is built.
	HTTPClient *http.Client
	// Debug, when true, dumps each request and response to DebugOut with
	// the Authorization header redacted.
	Debug bool
	// DebugOut is where debug output is written. nil => os.Stderr.
	DebugOut io.Writer
	// MaxRetries caps the number of 429 retries. 0 => defaultMaxRetries.
	// Negative values disable retry entirely.
	MaxRetries int
}

// Client talks to the Emailable v1 API.
type Client struct {
	httpClient  *http.Client
	baseURL     string
	accessToken string
	debug       bool
	debugOut    io.Writer
	maxRetries  int
}

// New returns a Client. When httpClient is nil a private *http.Client is
// constructed with a bounded per-request timeout; callers that need a
// different transport should pass their own.
func New(baseURL, accessToken string, httpClient *http.Client) *Client {
	return NewWithOptions(baseURL, accessToken, Options{HTTPClient: httpClient})
}

// NewWithOptions returns a Client configured per opts.
func NewWithOptions(baseURL, accessToken string, opts Options) *Client {
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: defaultRequestTimeout}
	}
	debugOut := opts.DebugOut
	if debugOut == nil {
		debugOut = os.Stderr
	}
	maxRetries := opts.MaxRetries
	if maxRetries == 0 {
		maxRetries = defaultMaxRetries
	} else if maxRetries < 0 {
		maxRetries = 0
	}
	return &Client{
		httpClient:  hc,
		baseURL:     baseURL,
		accessToken: accessToken,
		debug:       opts.Debug,
		debugOut:    debugOut,
		maxRetries:  maxRetries,
	}
}

// do issues an HTTP request with the configured auth headers and decodes a
// JSON response. Non-2xx responses return an *Error.
//
// 429 responses trigger an automatic retry honoring RateLimit-Reset, capped
// at c.maxRetries attempts. Each retry rebuilds the request from scratch
// since the form body Reader has already been consumed.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, form url.Values, out any) error {
	fullURL := strings.TrimRight(c.baseURL, "/") + path
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		var body io.Reader
		if len(form) > 0 {
			body = strings.NewReader(form.Encode())
		}

		req, err := http.NewRequestWithContext(ctx, method, fullURL, body)
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.accessToken)
		req.Header.Set("Accept", "application/json")
		if len(form) > 0 {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}

		c.dumpRequest(req)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("http: %w", err)
		}

		respBody, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return fmt.Errorf("read response: %w", err)
		}
		c.dumpResponse(resp, respBody)

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if out == nil {
				return nil
			}
			if err := json.Unmarshal(respBody, out); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
			return nil
		}

		apiErr := &Error{
			StatusCode: resp.StatusCode,
			Message:    extractMessage(respBody),
			Body:       respBody,
			RateLimit:  parseRateLimit(resp.Header),
		}
		lastErr = apiErr

		if resp.StatusCode != 429 || attempt == c.maxRetries {
			return apiErr
		}
		sleep := backoffFor(apiErr.RateLimit, resp.Header.Get("Retry-After"), attempt)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleep):
		}
	}
	return lastErr
}

// backoffFor picks how long to wait before retrying a 429. Prefers the
// IETF draft RateLimit-Reset header value, falls back to the older
// Retry-After header, then to an exponential default. A small random jitter
// is added so concurrent CLIs don't synchronize on the same retry instant.
// The result is clamped to [minRetrySleep, maxRetrySleep].
func backoffFor(rl *RateLimit, retryAfter string, attempt int) time.Duration {
	base := time.Duration(0)
	switch {
	case rl != nil && rl.Reset > 0:
		base = time.Duration(rl.Reset) * time.Second
	case retryAfter != "":
		if n, err := strconv.Atoi(retryAfter); err == nil && n > 0 {
			base = time.Duration(n) * time.Second
		}
	}
	if base == 0 {
		base = time.Duration(1<<attempt) * time.Second
	}
	// Jitter spreads out concurrent retries so CLIs don't synchronize.
	jitter := time.Duration(rand.IntN(250)) * time.Millisecond
	d := base + jitter
	if d < minRetrySleep {
		d = minRetrySleep
	}
	if d > maxRetrySleep {
		d = maxRetrySleep
	}
	return d
}

// dumpRequest writes the outgoing request to c.debugOut when debug is on, with
// the Authorization header redacted.
func (c *Client) dumpRequest(req *http.Request) {
	if !c.debug {
		return
	}
	// Clone so we can redact the Authorization header without mutating the
	// request that's about to fly.
	clone := req.Clone(req.Context())
	if clone.Header.Get("Authorization") != "" {
		clone.Header.Set("Authorization", "Bearer [redacted]")
	}
	dump, err := httputil.DumpRequestOut(clone, true)
	if err != nil {
		fmt.Fprintf(c.debugOut, "DEBUG: dump request: %v\n", err)
		return
	}
	fmt.Fprintf(c.debugOut, "\nDEBUG ==> outgoing request\n%s\n\n", indentLines(string(dump)))
}

// dumpResponse writes the response (with body) to c.debugOut when debug is on.
func (c *Client) dumpResponse(resp *http.Response, body []byte) {
	if !c.debug {
		return
	}
	// Splice the already-read body bytes back in so DumpResponse can emit them.
	clone := *resp
	clone.Body = io.NopCloser(strings.NewReader(string(body)))
	dump, err := httputil.DumpResponse(&clone, true)
	if err != nil {
		fmt.Fprintf(c.debugOut, "DEBUG: dump response: %v\n", err)
		return
	}
	fmt.Fprintf(c.debugOut, "DEBUG <== incoming response\n%s\n\n", indentLines(string(dump)))
}

// indentLines prefixes each line with two spaces so debug output is visually
// distinct from normal CLI text.
func indentLines(s string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}

// parseRateLimit reads the IETF draft `RateLimit-*` headers off h and returns
// a populated *RateLimit when at least one is present. Missing or unparseable
// values stay zero rather than failing.
func parseRateLimit(h http.Header) *RateLimit {
	limit := h.Get("RateLimit-Limit")
	remaining := h.Get("RateLimit-Remaining")
	reset := h.Get("RateLimit-Reset")
	if limit == "" && remaining == "" && reset == "" {
		return nil
	}
	rl := &RateLimit{}
	if n, err := strconv.Atoi(limit); err == nil {
		rl.Limit = n
	}
	if n, err := strconv.Atoi(remaining); err == nil {
		rl.Remaining = n
	}
	if n, err := strconv.Atoi(reset); err == nil {
		rl.Reset = n
	}
	return rl
}

// extractMessage pulls an error message from a JSON body, trying common keys
// in order: "message", "error", "error_description". Returns "" if none
// parsed successfully.
func extractMessage(body []byte) string {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	for _, key := range []string{"message", "error", "error_description"} {
		if v, ok := payload[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// VerifyOptions tunes a single-email real-time verification request. Each
// field omitted (nil pointer / zero value) lets the server pick its default.
type VerifyOptions struct {
	SMTP      *bool // nil => server default (true). false disables SMTP probing.
	AcceptAll *bool // nil => server default (false). true performs Accept-All detection.
	Timeout   int   // seconds, 2-10. 0 => server default (5).
}

// Verify performs a real-time verification of a single email via GET /verify.
func (c *Client) Verify(ctx context.Context, email string, opts *VerifyOptions) (*VerifyResult, error) {
	q := url.Values{}
	q.Set("email", email)
	if opts != nil {
		if opts.SMTP != nil {
			q.Set("smtp", strconv.FormatBool(*opts.SMTP))
		}
		if opts.AcceptAll != nil {
			q.Set("accept_all", strconv.FormatBool(*opts.AcceptAll))
		}
		if opts.Timeout != 0 {
			q.Set("timeout", strconv.Itoa(opts.Timeout))
		}
	}
	var out VerifyResult
	if err := c.do(ctx, http.MethodGet, "/verify", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SubmitBatchOptions tunes a batch verification submission. Each field
// omitted (zero value / nil pointer) lets the server pick its default.
type SubmitBatchOptions struct {
	URL            string   // optional webhook URL the server POSTs to on completion
	Retries        *bool    // nil => server default (true)
	ResponseFields []string // optional subset of result fields to return; nil => all
}

// SubmitBatch submits emails for batch verification via POST /batch and
// returns the new batch's id.
func (c *Client) SubmitBatch(ctx context.Context, emails []string, opts *SubmitBatchOptions) (*BatchSubmit, error) {
	form := url.Values{}
	form.Set("emails", strings.Join(emails, ","))
	if opts != nil {
		if opts.URL != "" {
			form.Set("url", opts.URL)
		}
		if opts.Retries != nil {
			form.Set("retries", strconv.FormatBool(*opts.Retries))
		}
		if len(opts.ResponseFields) > 0 {
			form.Set("response_fields", strings.Join(opts.ResponseFields, ","))
		}
	}
	var out BatchSubmit
	if err := c.do(ctx, http.MethodPost, "/batch", nil, form, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Batch fetches the current status (and, when complete or partial=true,
// per-email results) of a previously submitted batch via GET /batch?id=...
func (c *Client) Batch(ctx context.Context, id string, partial bool) (*BatchStatus, error) {
	q := url.Values{}
	q.Set("id", id)
	if partial {
		q.Set("partial", "true")
	}
	var out BatchStatus
	if err := c.do(ctx, http.MethodGet, "/batch", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Account fetches the authenticated user's account info via GET /account.
func (c *Client) Account(ctx context.Context) (*Account, error) {
	var out Account
	if err := c.do(ctx, http.MethodGet, "/account", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
