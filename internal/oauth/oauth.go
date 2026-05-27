// Package oauth implements the OAuth 2.0 device authorization grant
// (RFC 8628) client used by emailable-cli's login flow, talking to the
// /oauth/* endpoints.
//
// The package is transport-only: it does not touch the config file or surface
// UX.
package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// grantTypeDeviceCode is the RFC 8628 grant_type value used when exchanging
// a device_code for an access token.
const grantTypeDeviceCode = "urn:ietf:params:oauth:grant-type:device_code"

// minPollInterval is the floor for the device-code polling interval. RFC 8628
// §3.2 says clients MUST default to 5 seconds when the server omits interval.
const minPollInterval = 5 * time.Second

// defaultRequestTimeout caps a single OAuth HTTP call. Per-request rather than
// per-loop, since PollToken runs many requests over an authorization's
// lifetime, but bounded so a stuck socket can't wedge the login flow.
const defaultRequestTimeout = 30 * time.Second

// OAuth `error` field values the server may return from /oauth/token.
const (
	codeAuthorizationPending = "authorization_pending"
	codeSlowDown             = "slow_down"
	codeAccessDenied         = "access_denied"
	codeExpiredToken         = "expired_token"
	codeInvalidGrant         = "invalid_grant"
)

// Exported sentinel errors so callers can detect specific failure modes via
// errors.Is.
var (
	ErrAccessDenied = errors.New("oauth: access denied")
	ErrExpiredToken = errors.New("oauth: device code expired")
	// ErrInvalidGrant signals the server rejected the refresh_token as no
	// longer valid (rotated, revoked, or expired). Callers should treat this
	// as "the user must log in again" rather than retry.
	ErrInvalidGrant = errors.New("oauth: invalid grant")
)

// Client talks to the Emailable OAuth endpoints.
type Client struct {
	httpClient *http.Client
	appURL     string
	clientID   string

	// wait is overridable by tests so polling loops don't actually wait.
	// The default honors ctx so Ctrl+C during a long sleep returns
	// immediately instead of blocking for the full interval.
	wait func(ctx context.Context, d time.Duration) error
}

// NewClient returns a Client that posts to appURL with the given clientID.
// When httpClient is nil a private *http.Client is constructed with a
// bounded per-request timeout; callers that need a different transport
// (e.g. tests) should pass their own.
func NewClient(appURL, clientID string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultRequestTimeout}
	}
	return &Client{
		httpClient: httpClient,
		appURL:     appURL,
		clientID:   clientID,
		wait:       defaultWait,
	}
}

// defaultWait sleeps for d or returns ctx's error if ctx is cancelled first.
func defaultWait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// oauthError is the parsed `{ "error": ..., "error_description": ... }`
// body OAuth servers return on 4xx responses. Typed so callers can route on
// Code via errors.As.
type oauthError struct {
	Code        string
	Description string
}

func (e *oauthError) Error() string {
	if e.Description != "" {
		return e.Code + " (" + e.Description + ")"
	}
	return e.Code
}

// DeviceCode is the response from POST /oauth/device/code.
type DeviceCode struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// Token is the response from POST /oauth/token. The same shape is returned
// for both the device_code grant and refresh_token grant.
type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	CreatedAt    int64  `json:"created_at"`
}

// RequestDeviceCode returns a code pair whose user_code the human enters at
// verification_uri to authorize the CLI.
func (c *Client) RequestDeviceCode(ctx context.Context) (*DeviceCode, error) {
	form := url.Values{}
	form.Set("client_id", c.clientID)

	resp, err := c.formPost(ctx, "/oauth/device/code", form, "device code")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var dc DeviceCode
	if err := json.NewDecoder(resp.Body).Decode(&dc); err != nil {
		return nil, fmt.Errorf("oauth: decode device code: %w", err)
	}
	return &dc, nil
}

// PollToken exchanges a device_code for an access token. It loops until the
// server returns a token, the user denies, the code expires, or another
// non-pending error occurs. The polling interval starts at dc.Interval
// seconds and grows when the server signals slow_down.
func (c *Client) PollToken(ctx context.Context, dc *DeviceCode) (*Token, error) {
	form := url.Values{}
	form.Set("grant_type", grantTypeDeviceCode)
	form.Set("device_code", dc.DeviceCode)
	form.Set("client_id", c.clientID)

	interval := time.Duration(dc.Interval) * time.Second
	if interval < minPollInterval {
		interval = minPollInterval
	}
	for {
		tok, err := c.tokenPost(ctx, form)
		if err == nil {
			return tok, nil
		}

		var oerr *oauthError
		if errors.As(err, &oerr) {
			switch oerr.Code {
			case codeAuthorizationPending:
				if werr := c.wait(ctx, interval); werr != nil {
					return nil, werr
				}
				continue
			case codeSlowDown:
				interval += 5 * time.Second
				if werr := c.wait(ctx, interval); werr != nil {
					return nil, werr
				}
				continue
			case codeAccessDenied:
				return nil, wrapSentinel(ErrAccessDenied, oerr.Description)
			case codeExpiredToken:
				return nil, wrapSentinel(ErrExpiredToken, oerr.Description)
			}
		}
		return nil, err
	}
}

// Refresh sends no client_secret because the CLI is a public OAuth client.
//
// On an invalid_grant response (refresh token rotated, revoked, or expired)
// the returned error wraps ErrInvalidGrant so callers can detect a
// permanently-dead refresh token via errors.Is and prompt re-login. Other
// failures (network, 5xx, decode) propagate as-is.
func (c *Client) Refresh(ctx context.Context, refreshToken string) (*Token, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", c.clientID)
	tok, err := c.tokenPost(ctx, form)
	if err != nil {
		var oerr *oauthError
		if errors.As(err, &oerr) && oerr.Code == codeInvalidGrant {
			return nil, wrapSentinel(ErrInvalidGrant, oerr.Description)
		}
		return nil, err
	}
	return tok, nil
}

// Revoke invalidates accessToken via POST /oauth/revoke.
func (c *Client) Revoke(ctx context.Context, accessToken string) error {
	form := url.Values{}
	form.Set("token", accessToken)
	form.Set("client_id", c.clientID)

	resp, err := c.formPost(ctx, "/oauth/revoke", form, "revoke")
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// tokenPost wraps formPost with Token decoding. Server errors arrive as a
// wrapped *oauthError.
func (c *Client) tokenPost(ctx context.Context, form url.Values) (*Token, error) {
	resp, err := c.formPost(ctx, "/oauth/token", form, "token")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var tok Token
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return nil, fmt.Errorf("oauth: decode token: %w", err)
	}
	return &tok, nil
}

// formPost POSTs form to c.appURL+path as application/x-www-form-urlencoded.
// On 2xx returns the response with body still open; the caller must close
// it. On non-2xx closes the body internally and returns a parsed OAuth
// error. The op string ("device code", "token", "revoke") is included in
// the wrapped error message.
func (c *Client) formPost(ctx context.Context, path string, form url.Values, op string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.appURL+path,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return nil, fmt.Errorf("oauth: build %s request: %w", op, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth: %s: %w", op, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		return nil, parseOAuthError(resp, op)
	}
	return resp, nil
}

// wrapSentinel keeps the sentinel detectable via errors.Is while attaching
// the server's error_description for the human-readable message.
func wrapSentinel(sentinel error, description string) error {
	if description == "" {
		return sentinel
	}
	return fmt.Errorf("%w: %s", sentinel, description)
}

// parseOAuthError reads an OAuth-style error body from resp and returns it as
// a typed *oauthError so the server's `error` / `error_description` fields
// surface verbatim, without layering a package-specific prefix on top.
func parseOAuthError(resp *http.Response, op string) error {
	var body struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)

	if body.Error == "" {
		return fmt.Errorf("HTTP %d from %s endpoint", resp.StatusCode, op)
	}
	return &oauthError{
		Code:        body.Error,
		Description: body.ErrorDescription,
	}
}
