package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// End-to-end coverage of the batch subcommands against a stub server lives in
// batch_e2e_test.go (the API client base URL is env-routed by the harness in
// testutil_test.go). These tests stay focused on the --help wiring.
func TestBatchGet_Help(t *testing.T) {
	root := newRootCmd("dev")
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"batch", "get", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"--wait", "--partial", "--all", "--output", "BATCH_ID"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected help to mention %s, got %q", want, got)
		}
	}
}

// The old `batch status` and `batch results` subcommands were collapsed into
// `batch get` to mirror the single GET /v1/batch endpoint. Assert that the
// command tree doesn't expose them anymore. Walking Commands() is more
// reliable than substring-matching the help text (the words "status" and
// "results" still legitimately appear in `get`'s description).
func TestBatch_Help(t *testing.T) {
	root := newRootCmd("dev")
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"batch", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var batch *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "batch" {
			batch = c
			break
		}
	}
	if batch == nil {
		t.Fatal("batch command not registered")
	}

	have := map[string]bool{}
	for _, c := range batch.Commands() {
		have[c.Name()] = true
	}
	for _, want := range []string{"get", "verify"} {
		if !have[want] {
			t.Errorf("expected batch to register %q subcommand, have %v", want, have)
		}
	}
	for _, gone := range []string{"status", "results"} {
		if have[gone] {
			t.Errorf("expected batch NOT to register %q subcommand, have %v", gone, have)
		}
	}
}
