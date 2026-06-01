// Package oauth implements the OAuth 2.0 device authorization grant (RFC 8628) client.
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

const grantTypeDeviceCode = "urn:ietf:params:oauth:grant-type:device_code"

// minPollInterval: RFC 8628 §3.2 requires defaulting to 5 s when server omits interval.
const minPollInterval = 5 * time.Second

// defaultRequestTimeout is per-request (not per-loop) so a stuck socket can't wedge the login flow.
const defaultRequestTimeout = 30 * time.Second

const (
	codeAuthorizationPending = "authorization_pending"
	codeSlowDown             = "slow_down"
	codeAccessDenied         = "access_denied"
	codeExpiredToken         = "expired_token"
	codeInvalidGrant         = "invalid_grant"
)

var (
	// ErrAccessDenied is returned when the user explicitly denies the authorization request.
	ErrAccessDenied = errors.New("oauth: access denied")
	// ErrExpiredToken is returned when the device code has expired before the user authorized.
	ErrExpiredToken = errors.New("oauth: device code expired")
	// ErrInvalidGrant is returned when the refresh token was rotated, revoked, or expired; caller must re-login.
	ErrInvalidGrant = errors.New("oauth: invalid grant")
)

// Client performs OAuth 2.0 device authorization grant flows against appURL.
type Client struct {
	httpClient *http.Client
	appURL     string
	clientID   string

	// wait is overridable by tests; default is ctx-aware so Ctrl+C during a poll sleep returns immediately.
	wait func(ctx context.Context, d time.Duration) error
}

// NewClient returns a Client that posts to appURL with the given clientID.
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

// DeviceCode holds the server response from the device authorization endpoint.
type DeviceCode struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// Token holds the access and refresh tokens returned by the token endpoint.
type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	CreatedAt    int64  `json:"created_at"`
}

// RequestDeviceCode requests a device code from the authorization server.
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

// PollToken polls the token endpoint until the user authorizes, denies, or the code expires.
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

// Refresh sends no client_secret — the CLI is a public OAuth client.
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

// Revoke revokes the given access token at the authorization server.
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

func wrapSentinel(sentinel error, description string) error {
	if description == "" {
		return sentinel
	}
	return fmt.Errorf("%w: %s", sentinel, description)
}

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
