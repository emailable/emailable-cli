package api

import (
	"errors"
	"fmt"
)

// ErrUnauthenticated is returned when the API responds with a 401 status.
var ErrUnauthenticated = errors.New("api: not authenticated")

// RateLimit holds the parsed RateLimit-* response headers.
type RateLimit struct {
	Limit     int // requests allowed per window
	Remaining int // requests left in the current window
	Reset     int // Unix timestamp, in seconds, when the window resets
}

// Error represents a non-2xx API response.
type Error struct {
	StatusCode int
	Message    string
	Body       []byte
	RateLimit  *RateLimit // non-nil when response carried RateLimit-* headers
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("HTTP %d", e.StatusCode)
}

func (e *Error) Unwrap() error {
	if e.StatusCode == 401 {
		return ErrUnauthenticated
	}
	return nil
}
