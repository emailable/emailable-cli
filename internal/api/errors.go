package api

import (
	"errors"
	"fmt"
)

// ErrUnauthenticated is returned when the API responds 401, signalling the
// stored access token is invalid or expired.
var ErrUnauthenticated = errors.New("api: not authenticated")

// RateLimit captures the IETF draft `RateLimit-*` response headers. All fields
// are zero when the corresponding header was absent or unparseable.
type RateLimit struct {
	Limit     int // requests allowed per window
	Remaining int // requests left in the current window
	Reset     int // seconds until the window resets
}

// Error is the typed error returned for non-2xx API responses.
type Error struct {
	StatusCode int
	Message    string
	Body       []byte
	// RateLimit is set when the response carried any of the
	// `RateLimit-*` headers (most commonly on 429). nil otherwise.
	RateLimit *RateLimit
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("HTTP %d", e.StatusCode)
}

// Unwrap exposes ErrUnauthenticated for 401 responses.
func (e *Error) Unwrap() error {
	if e.StatusCode == 401 {
		return ErrUnauthenticated
	}
	return nil
}
