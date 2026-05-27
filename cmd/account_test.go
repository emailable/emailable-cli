package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// End-to-end coverage of `account status` against a stub server lives in
// account_e2e_test.go (the api client base URL is env-routed by the harness in
// testutil_test.go). This test stays focused on the --help wiring.
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
