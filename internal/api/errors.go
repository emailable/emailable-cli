package api

import (
	"errors"
	"fmt"
)

// ErrUnauthenticated is returned when the API responds 401, signalling the
// stored access token is invalid or expired. The login command checks for
// this via errors.Is and prompts the user to run `emailable login` again.
var ErrUnauthenticated = errors.New("api: not authenticated")

// RateLimit captures the IETF draft `RateLimit-*` response headers
// (RateLimit-Limit, RateLimit-Remaining, RateLimit-Reset). It is attached to
// *Error when present on the response so callers can surface retry hints to
// users (notably on 429). All fields are zero when the corresponding header
// was absent or unparseable; check Present to distinguish "no headers" from
// "headers all zero" — the Emailable API always sends at least RateLimit-Limit
// on rate-limited endpoints, so a populated Limit is a reliable signal too.
type RateLimit struct {
	Limit     int // requests allowed per window
	Remaining int // requests left in the current window
	Reset     int // seconds until the window resets
}

// Error is the typed error returned for non-2xx API responses. The wrapped
// ErrUnauthenticated (for 401) lets callers route on auth-failure via
// errors.Is(err, ErrUnauthenticated).
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

// Unwrap exposes ErrUnauthenticated for 401 responses so callers can write:
//
//	if errors.Is(err, api.ErrUnauthenticated) { ... }
func (e *Error) Unwrap() error {
	if e.StatusCode == 401 {
		return ErrUnauthenticated
	}
	return nil
}
