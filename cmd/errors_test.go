package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/emailable/emailable-cli/internal/api"
)

// TestRenderError_Human_APIError asserts the human-mode one-liner for an
// API error: `Error: <message> (HTTP <status>)`.
func TestRenderError_Human_APIError(t *testing.T) {
	var buf bytes.Buffer
	err := &api.Error{
		StatusCode: 422,
		Message:    "Invalid email",
		Body:       []byte(`{"message":"Invalid email"}`),
	}
	renderError(&buf, err, false)
	got := buf.String()
	want := "Error: Invalid email (HTTP 422)\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestRenderError_Human_RateLimit429 appends the retry hint when the API
// error is 429 with a known Reset window.
func TestRenderError_Human_RateLimit429(t *testing.T) {
	var buf bytes.Buffer
	err := &api.Error{
		StatusCode: 429,
		Message:    "Too Many Requests",
		Body:       []byte(`{"message":"Too Many Requests"}`),
		RateLimit:  &api.RateLimit{Limit: 1000, Remaining: 0, Reset: 60},
	}
	renderError(&buf, err, false)
	got := buf.String()
	want := "Error: Too Many Requests (HTTP 429) (retry in 60s)\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestRenderError_Human_Generic prints `Error: <msg>` for non-API errors
// (network, validation, config) with no status code decoration.
func TestRenderError_Human_Generic(t *testing.T) {
	var buf bytes.Buffer
	renderError(&buf, errors.New("dial tcp: connection refused"), false)
	got := buf.String()
	want := "Error: dial tcp: connection refused\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestRenderError_JSON_PassthroughBody confirms a JSON-object API body is
// passed through verbatim (flat, no envelope) when no rate-limit headers
// were captured.
func TestRenderError_JSON_PassthroughBody(t *testing.T) {
	var buf bytes.Buffer
	body := []byte(`{"message":"Invalid email","code":"bad_email"}`)
	err := &api.Error{StatusCode: 422, Message: "Invalid email", Body: body}
	renderError(&buf, err, true)

	got := strings.TrimRight(buf.String(), "\n")
	if !json.Valid([]byte(got)) {
		t.Fatalf("output is not valid JSON: %q", got)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["message"] != "Invalid email" {
		t.Errorf("expected message preserved, got %v", decoded["message"])
	}
	if decoded["code"] != "bad_email" {
		t.Errorf("expected code preserved, got %v", decoded["code"])
	}
	if _, wrapped := decoded["error"]; wrapped {
		t.Errorf("expected flat shape, got wrapped: %v", decoded)
	}
}

// TestRenderError_JSON_RateLimit429 merges rate_limit as a sibling key on
// the API body — no envelope.
func TestRenderError_JSON_RateLimit429(t *testing.T) {
	var buf bytes.Buffer
	err := &api.Error{
		StatusCode: 429,
		Message:    "Too Many Requests",
		Body:       []byte(`{"message":"Too Many Requests"}`),
		RateLimit:  &api.RateLimit{Limit: 1000, Remaining: 0, Reset: 60},
	}
	renderError(&buf, err, true)

	var payload struct {
		Message   string `json:"message"`
		Error     any    `json:"error"`
		RateLimit struct {
			Limit     int `json:"limit"`
			Remaining int `json:"remaining"`
			Reset     int `json:"reset"`
		} `json:"rate_limit"`
	}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if payload.Error != nil {
		t.Errorf("expected flat shape with no `error` envelope, got %v", payload.Error)
	}
	if payload.Message != "Too Many Requests" {
		t.Errorf("expected message preserved at top level, got %q", payload.Message)
	}
	if payload.RateLimit.Limit != 1000 || payload.RateLimit.Remaining != 0 || payload.RateLimit.Reset != 60 {
		t.Errorf("unexpected rate_limit: %+v", payload.RateLimit)
	}
}

// TestRenderError_JSON_NonJSONBody synthesizes a flat object when the API
// response body wasn't valid JSON (HTML 5xx page, empty body, etc).
func TestRenderError_JSON_NonJSONBody(t *testing.T) {
	var buf bytes.Buffer
	err := &api.Error{StatusCode: 502, Message: "", Body: []byte("<html>bad gateway</html>")}
	renderError(&buf, err, true)

	var payload map[string]any
	if jerr := json.Unmarshal(buf.Bytes(), &payload); jerr != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", jerr, buf.String())
	}
	if _, wrapped := payload["error"]; wrapped {
		t.Errorf("expected flat shape, got wrapped: %v", payload)
	}
	if code, _ := payload["status_code"].(float64); int(code) != 502 {
		t.Errorf("expected status_code 502, got %v", payload["status_code"])
	}
}

// TestRenderError_JSON_GenericError emits a flat object with just `message`
// (no `status_code`) for non-API errors.
func TestRenderError_JSON_GenericError(t *testing.T) {
	var buf bytes.Buffer
	renderError(&buf, errors.New("dial tcp: connection refused"), true)

	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if _, wrapped := payload["error"]; wrapped {
		t.Errorf("expected flat shape, got wrapped: %v", payload)
	}
	if msg, _ := payload["message"].(string); msg != "dial tcp: connection refused" {
		t.Errorf("expected message preserved, got %v", payload["message"])
	}
	if _, hasCode := payload["status_code"]; hasCode {
		t.Errorf("expected no status_code on non-API error, got %v", payload)
	}
}

// TestRenderError_JSON_NonObjectBody falls back to the synthesized flat
// shape when the API returns valid JSON that isn't an object (string,
// array, null, scalar) — we can't safely merge `rate_limit` into a
// non-object so the synthesized shape wins.
func TestRenderError_JSON_NonObjectBody(t *testing.T) {
	var buf bytes.Buffer
	err := &api.Error{StatusCode: 500, Message: "oops", Body: []byte(`"some string"`)}
	renderError(&buf, err, true)

	var payload map[string]any
	if jerr := json.Unmarshal(buf.Bytes(), &payload); jerr != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", jerr, buf.String())
	}
	if payload["message"] != "oops" {
		t.Errorf("expected message preserved, got %v", payload["message"])
	}
	if code, _ := payload["status_code"].(float64); int(code) != 500 {
		t.Errorf("expected status_code 500, got %v", payload["status_code"])
	}
}

// TestRenderError_Human_APIError_NoMessage falls back to `HTTP <code>` when
// the API body lacks a usable message.
func TestRenderError_Human_APIError_NoMessage(t *testing.T) {
	var buf bytes.Buffer
	renderError(&buf, &api.Error{StatusCode: 500}, false)
	got := buf.String()
	want := "Error: HTTP 500 (HTTP 500)\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestRenderError_Nil is a no-op.
func TestRenderError_Nil(t *testing.T) {
	var buf bytes.Buffer
	renderError(&buf, nil, false)
	if buf.Len() != 0 {
		t.Errorf("expected no output for nil error, got %q", buf.String())
	}
}

// TestRenderError_JSON_AddsCodeWhenAbsent confirms the CLI synthesizes a
// stable `code` field on API errors that don't ship one server-side.
func TestRenderError_JSON_AddsCodeWhenAbsent(t *testing.T) {
	var buf bytes.Buffer
	err := &api.Error{
		StatusCode: 422,
		Message:    "Invalid email",
		Body:       []byte(`{"message":"Invalid email"}`),
	}
	renderError(&buf, err, true)
	var decoded map[string]any
	if jerr := json.Unmarshal(buf.Bytes(), &decoded); jerr != nil {
		t.Fatalf("decode: %v", jerr)
	}
	if decoded["code"] != "invalid_input" {
		t.Errorf("expected code=invalid_input, got %v", decoded["code"])
	}
}

// TestRenderError_JSON_GenericHasCode confirms non-API errors still get a
// `code` field so agents can branch on it.
func TestRenderError_JSON_GenericHasCode(t *testing.T) {
	var buf bytes.Buffer
	renderError(&buf, errors.New("something broke"), true)
	var decoded map[string]any
	if jerr := json.Unmarshal(buf.Bytes(), &decoded); jerr != nil {
		t.Fatalf("decode: %v", jerr)
	}
	if decoded["code"] != "unknown" {
		t.Errorf("expected code=unknown, got %v", decoded["code"])
	}
}

// TestExitCode covers the full status-code → exit-code mapping. Each row
// pins down a specific contract documented in the README so we don't drift.
func TestExitCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, exitOK},
		{"not_authenticated_sentinel", errNotAuthenticated, exitAuth},
		{"api_401", &api.Error{StatusCode: 401}, exitAuth},
		{"api_403", &api.Error{StatusCode: 403}, exitAuth},
		{"api_404", &api.Error{StatusCode: 404}, exitInput},
		{"api_422", &api.Error{StatusCode: 422}, exitInput},
		{"api_429", &api.Error{StatusCode: 429}, exitRateLimit},
		{"api_500", &api.Error{StatusCode: 500}, exitNetwork},
		{"generic", errors.New("boom"), exitGeneric},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitCode(tc.err); got != tc.want {
				t.Errorf("exitCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
