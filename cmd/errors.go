package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"time"

	"github.com/emailable/emailable-cli/internal/api"
	"github.com/emailable/emailable-cli/internal/output"
	"github.com/spf13/cobra"
)

var errNotAuthenticated = errors.New("not logged in. Run `emailable login` first")

type invalidInputError struct{ msg string }

func (e *invalidInputError) Error() string { return e.msg }

// NewInvalidInput returns an error that marks user input as invalid.
func NewInvalidInput(msg string) error {
	return &invalidInputError{msg: msg}
}

// NewInvalidInputf returns a formatted invalid-input error.
func NewInvalidInputf(format string, args ...any) error {
	return &invalidInputError{msg: fmt.Sprintf(format, args...)}
}

func wrapInvalidInputArgs(fn cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := fn(cmd, args); err != nil {
			return NewInvalidInput(err.Error())
		}
		return nil
	}
}

// Exit codes — documented in the README; scripts can branch without parsing error messages.
const (
	exitOK        = 0
	exitGeneric   = 1
	exitAuth      = 2 // not_authenticated, forbidden
	exitRateLimit = 3 // rate_limited
	exitInput     = 4 // invalid_input, not_found
	exitNetwork   = 5 // network, server_error

	exitInterrupted = 130 // interrupted (SIGINT/SIGTERM), the shell convention for Ctrl-C
)

const (
	codeNotAuthenticated = "not_authenticated"
	codeForbidden        = "forbidden"
	codeNotFound         = "not_found"
	codeInvalidInput     = "invalid_input"
	codeRateLimited      = "rate_limited"
	codeTryAgain         = "try_again"
	codeServerError      = "server_error"
	codeNetwork          = "network"
	codeInterrupted      = "interrupted"
	codeUnknown          = "unknown"
)

func errorCode(err error) string {
	if err == nil {
		return ""
	}
	// Only a signal cancels the root context, so a canceled request is an
	// interrupt, not a network failure.
	if errors.Is(err, context.Canceled) {
		return codeInterrupted
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
	case codeInterrupted:
		return exitInterrupted
	default:
		return exitGeneric
	}
}

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

func renderError(w io.Writer, err error, jsonMode bool) {
	if err == nil {
		return
	}
	if jsonMode {
		renderJSONError(w, err)
		return
	}
	renderHumanError(w, err)
	var bwe *batchWaitError
	if errors.As(err, &bwe) {
		// Not quiet-aware: like the error line itself, this must always print.
		h := &output.Human{W: w}
		_ = h.Hint(fmt.Sprintf("Batch `%s` was submitted. Run `emailable batch get %s --wait` to fetch its results.", bwe.ID, bwe.ID))
	}
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
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(w, "Error: interrupted")
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
		if obj, ok := apiBodyAsObject(apiErr); ok {
			if apiErr.RateLimit != nil {
				obj["rate_limit"] = rateLimitMap(apiErr.RateLimit)
			}
			if _, hasCode := obj["code"]; !hasCode && code != "" {
				obj["code"] = code
			}
			writeJSONLine(w, withBatchID(obj, err))
			return
		}
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
		writeJSONLine(w, withBatchID(payload, err))
		return
	}
	msg := err.Error()
	if code == codeInterrupted {
		msg = "interrupted"
	}
	writeJSONLine(w, withBatchID(map[string]any{
		"message": msg,
		"code":    code,
	}, err))
}

// withBatchID adds "batch_id" when err belongs to a submitted batch, so a
// failed `--wait` in JSON mode still reports which batch to fetch.
func withBatchID(obj map[string]any, err error) map[string]any {
	var bwe *batchWaitError
	if errors.As(err, &bwe) {
		obj["batch_id"] = bwe.ID
	}
	return obj
}

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
		fmt.Fprintf(w, `{"code":%q,"message":%q}`+"\n", codeUnknown, err.Error())
		return
	}
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n"))
}
