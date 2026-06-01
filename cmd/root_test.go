package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootCommand_VersionFlag(t *testing.T) {
	cmd := newRootCmd("v0.1.0-test")

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := buf.String()
	if !strings.Contains(got, "v0.1.0-test") {
		t.Errorf("expected output to contain version string, got %q", got)
	}
	if !strings.Contains(got, "emailable") {
		t.Errorf("expected output to contain command name, got %q", got)
	}
}

func TestRootCommand_HelpFlag(t *testing.T) {
	cmd := newRootCmd("dev")

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := buf.String()
	if !strings.Contains(got, longDescription) {
		t.Errorf("expected help to include long description, got %q", got)
	}
}

// TestRootCommand_HelpListsPersistentFlags asserts that the persistent
// flags are surfaced in --help, both at the root and on a subcommand.
func TestRootCommand_HelpListsPersistentFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--help"},
		{"verify", "--help"},
	} {
		cmd := newRootCmd("dev")
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute %v: %v", args, err)
		}
		got := buf.String()
		for _, want := range []string{"--quiet", "-q"} {
			if !strings.Contains(got, want) {
				t.Errorf("args=%v: help missing %q\n--- help ---\n%s", args, want, got)
			}
		}
	}
}

func TestNewRootCmd_ResetsFlagState(t *testing.T) {
	jsonOutput = true
	jqExpr = ".version"
	jqQuery = mustCompile(t, ".version")
	apiKey = "sk_leaked"
	debugMode = true
	quietMode = true

	_ = newRootCmd("dev")

	if jsonOutput || jqExpr != "" || jqQuery != nil || apiKey != "" || debugMode || quietMode {
		t.Fatalf("expected root flag globals to reset, got json=%v jqExpr=%q jqQuery=%v apiKey=%q debug=%v quiet=%v",
			jsonOutput, jqExpr, jqQuery, apiKey, debugMode, quietMode)
	}
}
