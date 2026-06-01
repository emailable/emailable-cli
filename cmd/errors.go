package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"time"

	"github.com/emailable/emailable-cli/internal/api"
	"github.com/spf13/cobra"
)

// errNotAuthenticated is returned by requireAuth when the active env has no
// stored access token. Rendered as a plain message — there's no API status
// code to attach.
var errNotAuthenticated = errors.New("not logged in. Run `emailable login` first")

// invalidInputError carries a code=invalid_input classification through the
// error chain so renderError/exitCode can route local validation failures
// (bad email shape, missing file, malformed flag value, cobra positional-arg
// errors) to the documented invalid_input/exit-4 path. The wrapped message
// is rendered verbatim; no envelope is added.
type invalidInputError struct{ msg string }

func (e *invalidInputError) Error() string { return e.msg }

// NewInvalidInput returns an error tagged as code=invalid_input. The CLI's
// errorCode/exitCode plumbing maps this to "invalid_input" / exit 4 and the
// JSON renderer emits the standard flat `{"code":"invalid_input","message":...}`
// shape.
func NewInvalidInput(msg string) error {
	return &invalidInputError{msg: msg}
}

// NewInvalidInputf is the fmt.Errorf-style sibling of NewInvalidInput.
func NewInvalidInputf(format string, args ...any) error {
	return &invalidInputError{msg: fmt.Sprintf(format, args...)}
}

// wrapInvalidInputArgs adapts a cobra positional-args validator so its error
// (e.g. "accepts 1 arg(s), received 0") flows through the invalid_input
// classification instead of cobra's default exit-1 path.
func wrapInvalidInputArgs(fn cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := fn(cmd, args); err != nil {
			return NewInvalidInput(err.Error())
		}
		return nil
	}
}

// Exit codes. Documented in the README so scripts and AI agents can branch
// on the specific failure mode without parsing the error message.
const (
	exitOK        = 0
	exitGeneric   = 1
	exitAuth      = 2 // not_authenticated, forbidden
	exitRateLimit = 3 // rate_limited
	exitInput     = 4 // invalid_input, not_found
	exitNetwork   = 5 // network, server_error
)

// Stable error code values. Emitted as the top-level `code` field in JSON
// error output. The CLI prefers an API-provided `code` when present in the
// response body; otherwise these are derived from HTTP status / error type.
const (
	codeNotAuthenticated = "not_authenticated"
	codeForbidden        = "forbidden"
	codeNotFound         = "not_found"
	codeInvalidInput     = "invalid_input"
	codeRateLimited      = "rate_limited"
	codeTryAgain         = "try_again"
	codeServerError      = "server_error"
	codeNetwork          = "network"
	codeUnknown          = "unknown"
)

// errorCode returns the stable CLI error code for err. Pure function of the
// error value — does not consult the API body. Used both to populate the
// JSON output's `code` field (when the body didn't already carry one) and
// to drive exit-code classification.
func errorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, errNotAuthenticated) || errors.Is(err, api.ErrUnauthenticated) {
		return codeNotAuthenticated
	}
	var iie *invalidInputError
	if errors.As(err, &iie) {
		return codeInvalidInput
	}
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == 401:
			return codeNotAuthenticated
		case apiErr.StatusCode == 403:
			return codeForbidden
		case apiErr.StatusCode == 404:
			return codeNotFound
		case apiErr.StatusCode == 429:
			return codeRateLimited
		case apiErr.StatusCode == 249:
			return codeTryAgain
		case apiErr.StatusCode == 400 || apiErr.StatusCode == 422:
			return codeInvalidInput
		case apiErr.StatusCode >= 500:
			return codeServerError
		}
	}
	if isNetworkError(err) {
		return codeNetwork
	}
	return codeUnknown
}

// exitCode maps an error to a documented process exit code. See the exit*
// constants for the taxonomy.
func exitCode(err error) int {
	if err == nil {
		return exitOK
	}
	switch errorCode(err) {
	case codeNotAuthenticated, codeForbidden:
		return exitAuth
	case codeRateLimited, codeTryAgain:
		return exitRateLimit
	case codeInvalidInput, codeNotFound:
		return exitInput
	case codeNetwork, codeServerError:
		return exitNetwork
	default:
		return exitGeneric
	}
}

// isNetworkError reports whether err is a network/transport failure rather
// than an HTTP response. Matches *url.Error (DNS/dial/timeout), net.Error,
// and *net.OpError. We deliberately don't match arbitrary errors — only
// connection-shaped ones — so misclassification stays narrow.
func isNetworkError(err error) bool {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

// renderError writes a terminal-friendly representation of err to w.
//
// JSON mode: emits a single line of JSON to stderr, always flat (no envelope).
// For *api.Error with a JSON-object body it passes the body through verbatim,
// merging in a `rate_limit` field when rate-limit headers were captured. For
// non-object / non-JSON bodies it synthesizes a flat object:
//
//	{"message":"...","status_code":N,"code":"..."}
//
// A stable `code` field is always added (preserving any code the API
// returned). For non-API errors (network, config, validation) it emits a
// flat object with `message` + `code`. The shape is intentionally consistent
// across paths so agents can parse a single schema.
//
// Human mode: prints `Error: <message> (HTTP <status>)` for *api.Error and
// `Error: <message>` otherwise. On 429 with a known reset timestamp we append a
// retry hint so the user knows when to retry.
func renderError(w io.Writer, err error, jsonMode bool) {
	if err == nil {
		return
	}
	if jsonMode {
		renderJSONError(w, err)
		return
	}
	renderHumanError(w, err)
}

func renderHumanError(w io.Writer, err error) {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		msg := apiErr.Message
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", apiErr.StatusCode)
		}
		if apiErr.StatusCode == 249 {
			fmt.Fprintf(w, "Pending: %s\n", msg)
			return
		}
		line := fmt.Sprintf("Error: %s (HTTP %d)", msg, apiErr.StatusCode)
		if apiErr.StatusCode == 429 {
			if resetAfter := rateLimitResetAfter(apiErr.RateLimit, time.Now()); resetAfter > 0 {
				line += fmt.Sprintf(" (retry in %ds)", int(resetAfter.Round(time.Second)/time.Second))
			}
		}
		fmt.Fprintln(w, line)
		return
	}
	fmt.Fprintf(w, "Error: %s\n", err.Error())
}

func rateLimitResetAfter(rl *api.RateLimit, now time.Time) time.Duration {
	if rl == nil || rl.Reset <= 0 {
		return 0
	}
	resetAt := time.Unix(int64(rl.Reset), 0)
	if !resetAt.After(now) {
		return 0
	}
	return resetAt.Sub(now)
}

func renderJSONError(w io.Writer, err error) {
	code := errorCode(err)

	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		// API body is a JSON object: pass it through, merging rate_limit
		// and code as sibling top-level fields. The API's own `code` (if
		// present) wins so callers see the canonical server-side value.
		if obj, ok := apiBodyAsObject(apiErr); ok {
			if apiErr.RateLimit != nil {
				obj["rate_limit"] = rateLimitMap(apiErr.RateLimit)
			}
			if _, hasCode := obj["code"]; !hasCode && code != "" {
				obj["code"] = code
			}
			writeJSONLine(w, obj)
			return
		}
		// Body wasn't a JSON object (HTML, empty, scalar, array): synthesize
		// a flat object.
		msg := apiErr.Message
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", apiErr.StatusCode)
		}
		payload := map[string]any{
			"message":     msg,
			"status_code": apiErr.StatusCode,
			"code":        code,
		}
		if apiErr.RateLimit != nil {
			payload["rate_limit"] = rateLimitMap(apiErr.RateLimit)
		}
		writeJSONLine(w, payload)
		return
	}
	// Non-API errors (network, config, validation): flat object with `message`
	// and `code`. No status_code because there isn't one.
	writeJSONLine(w, map[string]any{
		"message": err.Error(),
		"code":    code,
	})
}

// apiBodyAsObject parses the API error body as a JSON object. Returns the
// decoded map and true on success; false if the body is empty, not valid
// JSON, or valid JSON but not an object (string, number, array, null).
func apiBodyAsObject(e *api.Error) (map[string]any, bool) {
	if len(e.Body) == 0 {
		return nil, false
	}
	var obj map[string]any
	if err := json.Unmarshal(e.Body, &obj); err != nil || obj == nil {
		return nil, false
	}
	return obj, true
}

func rateLimitMap(rl *api.RateLimit) map[string]int {
	return map[string]int{
		"limit":     rl.Limit,
		"remaining": rl.Remaining,
		"reset":     rl.Reset,
	}
}

func writeJSONLine(w io.Writer, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		// Should never happen for the shapes we build, but fall back to the
		// same flat shape used by every other error path so consumers see a
		// consistent schema instead of an unexpected envelope.
		fmt.Fprintf(w, `{"code":%q,"message":%q}`+"\n", codeUnknown, err.Error())
		return
	}
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n"))
}
