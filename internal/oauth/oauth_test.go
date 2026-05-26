package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRequestDeviceCode_HappyPath(t *testing.T) {
	var (
		gotMethod      string
		gotPath        string
		gotContentType string
		gotBody        string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":               "device-abc123",
			"user_code":                 "WDJB-MJHT",
			"verification_uri":          "https://app.emailable.com/oauth/device",
			"verification_uri_complete": "https://app.emailable.com/oauth/device?user_code=WDJB-MJHT",
			"expires_in":                900,
			"interval":                  5,
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-client-id", server.Client())

	dc, err := client.RequestDeviceCode(context.Background())
	if err != nil {
		t.Fatalf("RequestDeviceCode: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("HTTP method: got %q, want POST", gotMethod)
	}
	if gotPath != "/oauth/device/code" {
		t.Errorf("path: got %q, want /oauth/device/code", gotPath)
	}
	if !strings.Contains(gotContentType, "application/x-www-form-urlencoded") {
		t.Errorf("Content-Type: got %q, want application/x-www-form-urlencoded", gotContentType)
	}
	if !strings.Contains(gotBody, "client_id=test-client-id") {
		t.Errorf("request body: got %q, want client_id=test-client-id", gotBody)
	}

	if dc.DeviceCode != "device-abc123" {
		t.Errorf("DeviceCode: got %q, want device-abc123", dc.DeviceCode)
	}
	if dc.UserCode != "WDJB-MJHT" {
		t.Errorf("UserCode: got %q, want WDJB-MJHT", dc.UserCode)
	}
	if dc.VerificationURI != "https://app.emailable.com/oauth/device" {
		t.Errorf("VerificationURI: got %q", dc.VerificationURI)
	}
	if dc.VerificationURIComplete != "https://app.emailable.com/oauth/device?user_code=WDJB-MJHT" {
		t.Errorf("VerificationURIComplete: got %q", dc.VerificationURIComplete)
	}
	if dc.ExpiresIn != 900 {
		t.Errorf("ExpiresIn: got %d, want 900", dc.ExpiresIn)
	}
	if dc.Interval != 5 {
		t.Errorf("Interval: got %d, want 5", dc.Interval)
	}
}

func TestPollToken_Success(t *testing.T) {
	var (
		gotMethod      string
		gotPath        string
		gotContentType string
		gotBody        string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-xyz",
			"refresh_token": "refresh-abc",
			"token_type":    "Bearer",
			"expires_in":    86400,
			"scope":         "all",
			"created_at":    1748000000,
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-client-id", server.Client())
	dc := &DeviceCode{DeviceCode: "device-abc123", Interval: 5}

	tok, err := client.PollToken(context.Background(), dc)
	if err != nil {
		t.Fatalf("PollToken: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("HTTP method: got %q, want POST", gotMethod)
	}
	if gotPath != "/oauth/token" {
		t.Errorf("path: got %q, want /oauth/token", gotPath)
	}
	if !strings.Contains(gotContentType, "application/x-www-form-urlencoded") {
		t.Errorf("Content-Type: got %q, want application/x-www-form-urlencoded", gotContentType)
	}
	if !strings.Contains(gotBody, "grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Adevice_code") {
		t.Errorf("body missing grant_type, got %q", gotBody)
	}
	if !strings.Contains(gotBody, "device_code=device-abc123") {
		t.Errorf("body missing device_code, got %q", gotBody)
	}
	if !strings.Contains(gotBody, "client_id=test-client-id") {
		t.Errorf("body missing client_id, got %q", gotBody)
	}

	if tok.AccessToken != "access-xyz" {
		t.Errorf("AccessToken: got %q", tok.AccessToken)
	}
	if tok.RefreshToken != "refresh-abc" {
		t.Errorf("RefreshToken: got %q", tok.RefreshToken)
	}
	if tok.TokenType != "Bearer" {
		t.Errorf("TokenType: got %q", tok.TokenType)
	}
	if tok.ExpiresIn != 86400 {
		t.Errorf("ExpiresIn: got %d", tok.ExpiresIn)
	}
	if tok.Scope != "all" {
		t.Errorf("Scope: got %q", tok.Scope)
	}
	if tok.CreatedAt != 1748000000 {
		t.Errorf("CreatedAt: got %d", tok.CreatedAt)
	}
}

func TestPollToken_RetriesOnAuthorizationPending(t *testing.T) {
	var (
		callCount int
		sleeps    []time.Duration
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount < 3 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-final",
			"refresh_token": "refresh-final",
			"token_type":    "Bearer",
			"expires_in":    86400,
			"scope":         "all",
			"created_at":    1748000000,
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-client-id", server.Client())
	client.wait = func(_ context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		return nil
	}

	dc := &DeviceCode{DeviceCode: "device-abc123", Interval: 5}
	tok, err := client.PollToken(context.Background(), dc)
	if err != nil {
		t.Fatalf("PollToken: %v", err)
	}

	if tok.AccessToken != "access-final" {
		t.Errorf("AccessToken: got %q, want access-final", tok.AccessToken)
	}
	if callCount != 3 {
		t.Errorf("call count: got %d, want 3", callCount)
	}
	if len(sleeps) != 2 {
		t.Fatalf("sleeps: got %d, want 2", len(sleeps))
	}
	for i, s := range sleeps {
		if s != 5*time.Second {
			t.Errorf("sleep[%d]: got %v, want 5s", i, s)
		}
	}
}

func TestPollToken_ZeroIntervalDefaultsToFiveSeconds(t *testing.T) {
	var (
		callCount int
		sleeps    []time.Duration
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-final",
			"refresh_token": "refresh-final",
			"token_type":    "Bearer",
			"expires_in":    86400,
			"scope":         "all",
			"created_at":    1748000000,
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-client-id", server.Client())
	client.wait = func(_ context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		return nil
	}

	// Server omitted interval (parsed as 0). RFC 8628 says default to 5s.
	dc := &DeviceCode{DeviceCode: "device-abc123", Interval: 0}
	if _, err := client.PollToken(context.Background(), dc); err != nil {
		t.Fatalf("PollToken: %v", err)
	}

	if len(sleeps) != 1 {
		t.Fatalf("sleeps: got %d, want 1", len(sleeps))
	}
	if sleeps[0] != 5*time.Second {
		t.Errorf("sleep[0]: got %v, want 5s (RFC 8628 default)", sleeps[0])
	}
}

func TestPollToken_SlowDownIncreasesInterval(t *testing.T) {
	var (
		callCount int
		sleeps    []time.Duration
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		switch callCount {
		case 1:
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "slow_down"})
		case 2:
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "access-final",
				"refresh_token": "refresh-final",
				"token_type":    "Bearer",
				"expires_in":    86400,
				"scope":         "all",
				"created_at":    1748000000,
			})
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-client-id", server.Client())
	client.wait = func(_ context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		return nil
	}

	dc := &DeviceCode{DeviceCode: "device-abc123", Interval: 5}
	tok, err := client.PollToken(context.Background(), dc)
	if err != nil {
		t.Fatalf("PollToken: %v", err)
	}

	if tok.AccessToken != "access-final" {
		t.Errorf("AccessToken: got %q", tok.AccessToken)
	}
	if callCount != 3 {
		t.Errorf("call count: got %d, want 3", callCount)
	}
	if len(sleeps) != 2 {
		t.Fatalf("sleeps: got %d, want 2", len(sleeps))
	}
	if sleeps[0] != 10*time.Second {
		t.Errorf("sleeps[0]: got %v, want 10s (interval bumped by slow_down)", sleeps[0])
	}
	if sleeps[1] != 10*time.Second {
		t.Errorf("sleeps[1]: got %v, want 10s (bump persists)", sleeps[1])
	}
}

func TestPollToken_AccessDenied(t *testing.T) {
	var sleeps []time.Duration

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "access_denied"})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-client-id", server.Client())
	client.wait = func(_ context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		return nil
	}

	dc := &DeviceCode{DeviceCode: "device-abc123", Interval: 5}
	_, err := client.PollToken(context.Background(), dc)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrAccessDenied) {
		t.Errorf("expected ErrAccessDenied, got %v", err)
	}
	if len(sleeps) != 0 {
		t.Errorf("expected no sleeps (no retry), got %d", len(sleeps))
	}
}

func TestPollToken_ExpiredToken(t *testing.T) {
	var sleeps []time.Duration

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "expired_token"})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-client-id", server.Client())
	client.wait = func(_ context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		return nil
	}

	dc := &DeviceCode{DeviceCode: "device-abc123", Interval: 5}
	_, err := client.PollToken(context.Background(), dc)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrExpiredToken) {
		t.Errorf("expected ErrExpiredToken, got %v", err)
	}
	if len(sleeps) != 0 {
		t.Errorf("expected no sleeps (no retry), got %d", len(sleeps))
	}
}

func TestRefresh_Success(t *testing.T) {
	var (
		gotMethod      string
		gotPath        string
		gotContentType string
		gotBody        string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-access",
			"refresh_token": "new-refresh",
			"token_type":    "Bearer",
			"expires_in":    86400,
			"scope":         "all",
			"created_at":    1748100000,
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-client-id", server.Client())

	tok, err := client.Refresh(context.Background(), "old-refresh-token")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("HTTP method: got %q, want POST", gotMethod)
	}
	if gotPath != "/oauth/token" {
		t.Errorf("path: got %q, want /oauth/token", gotPath)
	}
	if !strings.Contains(gotContentType, "application/x-www-form-urlencoded") {
		t.Errorf("Content-Type: got %q, want application/x-www-form-urlencoded", gotContentType)
	}
	if !strings.Contains(gotBody, "grant_type=refresh_token") {
		t.Errorf("body missing grant_type=refresh_token, got %q", gotBody)
	}
	if !strings.Contains(gotBody, "refresh_token=old-refresh-token") {
		t.Errorf("body missing refresh_token=old-refresh-token, got %q", gotBody)
	}
	if !strings.Contains(gotBody, "client_id=test-client-id") {
		t.Errorf("body missing client_id, got %q", gotBody)
	}

	if tok.AccessToken != "new-access" {
		t.Errorf("AccessToken: got %q, want new-access", tok.AccessToken)
	}
	if tok.RefreshToken != "new-refresh" {
		t.Errorf("RefreshToken: got %q, want new-refresh", tok.RefreshToken)
	}
}

func TestRefresh_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":             "invalid_grant",
			"error_description": "The refresh token is invalid.",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-client-id", server.Client())

	_, err := client.Refresh(context.Background(), "stale-refresh-token")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrInvalidGrant) {
		t.Errorf("expected error to wrap ErrInvalidGrant, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "The refresh token is invalid.") {
		t.Errorf("expected error to include error_description, got %q", err.Error())
	}
}

func TestRevoke_Success(t *testing.T) {
	var (
		gotMethod      string
		gotPath        string
		gotContentType string
		gotBody        string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-client-id", server.Client())

	err := client.Revoke(context.Background(), "access-to-revoke")
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("HTTP method: got %q, want POST", gotMethod)
	}
	if gotPath != "/oauth/revoke" {
		t.Errorf("path: got %q, want /oauth/revoke", gotPath)
	}
	if !strings.Contains(gotContentType, "application/x-www-form-urlencoded") {
		t.Errorf("Content-Type: got %q, want application/x-www-form-urlencoded", gotContentType)
	}
	if !strings.Contains(gotBody, "token=access-to-revoke") {
		t.Errorf("body missing token, got %q", gotBody)
	}
	if !strings.Contains(gotBody, "client_id=test-client-id") {
		t.Errorf("body missing client_id, got %q", gotBody)
	}
}

func TestRevoke_AcceptsNoContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-client-id", server.Client())

	if err := client.Revoke(context.Background(), "access-to-revoke"); err != nil {
		t.Errorf("Revoke with 204 should succeed, got error: %v", err)
	}
}

func TestRevoke_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "invalid_token",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-client-id", server.Client())

	err := client.Revoke(context.Background(), "stale-access-token")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid_token") {
		t.Errorf("expected error to surface 'invalid_token', got %q", err.Error())
	}
}

func TestRequestDeviceCode_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "invalid_client",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "bad-client-id", server.Client())

	_, err := client.RequestDeviceCode(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid_client") {
		t.Errorf("expected error to surface the OAuth error code, got %q", err.Error())
	}
}
