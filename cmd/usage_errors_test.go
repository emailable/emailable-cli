package cmd

import (
	"net/http"
	"strings"
	"testing"
)

func assertInvalidInput(t *testing.T, res *runResult) {
	t.Helper()
	if res.Err == nil {
		t.Fatal("expected an error")
	}
	if got := errorCode(res.Err); got != codeInvalidInput {
		t.Errorf("errorCode: got %q want %q (err: %v)", got, codeInvalidInput, res.Err)
	}
	if got := exitCode(res.Err); got != exitInput {
		t.Errorf("exitCode: got %d want %d", got, exitInput)
	}
}

func TestUsageErrors_InvalidInput(t *testing.T) {
	cases := [][]string{
		{"bogus"},
		{"batch", "bogus"},
		{"account", "bogus"},
		{"skill", "bogus"},
		{"--bogus"},
		{"batch", "get", "--bogus", "bch_1"},
		{"verify", "--timeout", "abc", "a@x.com"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			newTestEnv(t, http.NotFoundHandler())
			assertInvalidInput(t, runRoot(t, args...))
		})
	}
}

func TestUsageErrors_UnknownCommandSuggests(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())
	res := runRoot(t, "bacth")
	assertInvalidInput(t, res)
	if !strings.Contains(res.Err.Error(), `did you mean "batch"`) {
		t.Errorf("expected suggestion, got %q", res.Err)
	}
}

func TestUsageErrors_GroupWithoutArgsShowsHelp(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())
	res := runRoot(t, "batch")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	if !strings.Contains(res.Stdout.String(), "USAGE") {
		t.Errorf("expected help, got %q", res.Stdout.String())
	}
}

func TestUsageErrors_UnknownCommandHonorsJQ(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())
	res := runRoot(t, "bogus", "--jq", ".")
	assertInvalidInput(t, res)
	if !jsonOutput {
		t.Error("expected --jq to switch the error to JSON mode")
	}
}

func TestJSONRequested(t *testing.T) {
	cases := []struct {
		args           []string
		want, explicit bool
	}{
		{[]string{"--bogus", "--json"}, true, true},
		{[]string{"--json=false", "--bogus"}, false, true},
		{[]string{"--bogus", "--jq", ".x"}, true, true},
		{[]string{"--bogus", "--jq=.x"}, true, true},
		{[]string{"--bogus"}, false, false},
		{[]string{"--", "--json"}, false, false},
	}
	for _, tc := range cases {
		want, explicit := jsonRequested(tc.args)
		if want != tc.want || explicit != tc.explicit {
			t.Errorf("jsonRequested(%v) = %v,%v want %v,%v", tc.args, want, explicit, tc.want, tc.explicit)
		}
	}
}

func TestFlagUsageError_JSONAfterBadFlag(t *testing.T) {
	newTestEnv(t, http.NotFoundHandler())
	prev := cliArgs
	cliArgs = func() []string { return []string{"--bogus", "--json"} }
	t.Cleanup(func() { cliArgs = prev })

	res := runRoot(t, "--bogus", "--json")
	assertInvalidInput(t, res)
	if !jsonOutput {
		t.Error("expected --json after the bad flag to enable JSON errors")
	}
}
