// Package api is the HTTP client for the Emailable v1 REST API.
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

// defaultRequestTimeout is generous because real-time verify can spend ~30s SMTP-probing slow MX hosts.
const defaultRequestTimeout = 60 * time.Second

const (
	defaultMaxRetries = 2
	maxRetrySleep     = 60 * time.Second
	minRetrySleep     = 500 * time.Millisecond
)

// Options tunes a Client. All fields are optional.
type Options struct {
	HTTPClient *http.Client // nil => private client with defaultRequestTimeout
	Debug      bool
	DebugOut   io.Writer // nil => os.Stderr
	MaxRetries int       // 0 => defaultMaxRetries; negative => disable retry
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

// New returns a Client for baseURL authenticated with accessToken.
func New(baseURL, accessToken string, httpClient *http.Client) *Client {
	return NewWithOptions(baseURL, accessToken, Options{HTTPClient: httpClient})
}

// NewWithOptions returns a Client for baseURL authenticated with accessToken, applying opts.
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

func (c *Client) do(ctx context.Context, method, path string, query url.Values, form url.Values, out any) error {
	fullURL := strings.TrimRight(c.baseURL, "/") + path
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		// Rebuilt each attempt: the form body Reader is single-use, so a retry
		// needs a fresh one.
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

		if resp.StatusCode >= 200 && resp.StatusCode < 300 && !isRetryableStatus(resp.StatusCode) {
			if out == nil {
				return nil
			}
			if err := json.Unmarshal(respBody, out); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
			if rr, ok := out.(rawReceiver); ok {
				rr.setRaw(respBody)
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

		if !isRetryableStatus(resp.StatusCode) || attempt == c.maxRetries {
			return apiErr
		}
		sleep := backoffFor(apiErr.RateLimit, attempt, time.Now())
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleep):
		}
	}
	return lastErr
}

func isRetryableStatus(status int) bool {
	return status == 249 || status == http.StatusTooManyRequests
}

func backoffFor(rl *RateLimit, attempt int, now time.Time) time.Duration {
	base := time.Duration(0)
	if rl != nil && rl.Reset > 0 {
		resetAt := time.Unix(int64(rl.Reset), 0)
		if resetAt.After(now) {
			base = resetAt.Sub(now)
		}
	}
	if base == 0 {
		base = time.Duration(1<<attempt) * time.Second
	}
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

func (c *Client) dumpRequest(req *http.Request) {
	if !c.debug {
		return
	}
	// Clone so we can redact Authorization without mutating the in-flight request.
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

func (c *Client) dumpResponse(resp *http.Response, body []byte) {
	if !c.debug {
		return
	}
	// Splice the already-read body back in so DumpResponse can emit it.
	clone := *resp
	clone.Body = io.NopCloser(strings.NewReader(string(body)))
	dump, err := httputil.DumpResponse(&clone, true)
	if err != nil {
		fmt.Fprintf(c.debugOut, "DEBUG: dump response: %v\n", err)
		return
	}
	fmt.Fprintf(c.debugOut, "DEBUG <== incoming response\n%s\n\n", indentLines(string(dump)))
}

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

// VerifyOptions controls optional parameters for the Verify request.
type VerifyOptions struct {
	SMTP      *bool // nil => server default
	AcceptAll *bool // nil => server default
	Timeout   int   // seconds 2-10; 0 => server default
}

// Verify verifies a single email address via GET /verify.
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

// SubmitBatchOptions controls optional parameters for the SubmitBatch request.
type SubmitBatchOptions struct {
	URL            string   // webhook URL; empty => none
	Retries        *bool    // nil => server default
	ResponseFields []string // nil => all fields
}

// SubmitBatch submits a list of emails for batch verification via POST /batch.
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

// Batch fetches the status of a batch by id via GET /batch.
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

// Account fetches the authenticated account details via GET /account.
func (c *Client) Account(ctx context.Context) (*Account, error) {
	var out Account
	if err := c.do(ctx, http.MethodGet, "/account", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
