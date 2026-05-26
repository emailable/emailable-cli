package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// End-to-end testing of `account status` would require the api client to talk
// to a configurable base URL. As-is, the client base URL is derived from the
// fixed env.Current(). Refactoring that is out of scope here, so we limit
// ourselves to verifying the --help wiring of the subcommand.
func TestAccountStatus_Help(t *testing.T) {
	root := newRootCmd("dev")
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"account", "status", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "Show owner email and remaining credits") {
		t.Errorf("expected help to describe purpose, got %q", got)
	}
}

func TestAccount_Help(t *testing.T) {
	root := newRootCmd("dev")
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"account", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "status") {
		t.Errorf("expected help to list status subcommand, got %q", got)
	}
}
