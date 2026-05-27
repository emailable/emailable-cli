package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestIsFirstRun covers the gating that decides whether a bare `emailable`
// launches the getting-started flow. terminalsInteractive is stubbed so the
// TTY branch is exercised without a real PTY.
func TestIsFirstRun(t *testing.T) {
	cases := []struct {
		name        string
		interactive bool
		json        bool
		quiet       bool
		loggedIn    bool
		want        bool
	}{
		{"interactive and logged out", true, false, false, false, true},
		{"not a terminal", false, false, false, false, false},
		{"json mode", true, true, false, false, false},
		{"quiet mode", true, false, true, false, false},
		{"already logged in", true, false, false, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, http.NotFoundHandler())
			if tc.loggedIn {
				env.seedAPIKey(t, "sk_test_xxx")
			}
			jsonOutput = tc.json
			quietMode = tc.quiet

			orig := terminalsInteractive
			terminalsInteractive = func(*cobra.Command) bool { return tc.interactive }
			t.Cleanup(func() { terminalsInteractive = orig })

			cctx, err := newCmdCtx(false)
			if err != nil {
				t.Fatalf("newCmdCtx: %v", err)
			}
			if got := isFirstRun(cctx, &cobra.Command{}); got != tc.want {
				t.Errorf("isFirstRun = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestKeyRejected covers which API statuses re-prompt for a new key versus
// propagate: only the auth-refusal statuses (400/401/403) retry. Notably 402
// (valid key, out of credits) must propagate so it can't trap the user.
func TestKeyRejected(t *testing.T) {
	cases := map[int]bool{
		400: true, 401: true, 403: true,
		402: false, 404: false, 408: false, 422: false, 429: false,
		200: false, 500: false, 503: false,
	}
	for status, want := range cases {
		if got := keyRejected(status); got != want {
			t.Errorf("keyRejected(%d) = %v, want %v", status, got, want)
		}
	}
}

// TestRootBareShowsHelp asserts that a bare invocation prints help in a
// non-interactive context (stdout is a buffer, so terminalsInteractive is
// false) rather than launching onboarding — the guarantee for scripts and CI.
func TestRootBareShowsHelp(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())

	res := runRoot(t)
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	if out := res.Stdout.String(); !strings.Contains(out, "USAGE") {
		t.Errorf("expected help output for bare invocation, got:\n%s", out)
	}
}
