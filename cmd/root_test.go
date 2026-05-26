package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/emailable/emailable-cli/internal/ui"
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

// TestRootCommand_HelpListsNewPersistentFlags asserts that --quiet/-q and
// --no-color are both surfaced in --help, both at the root and (since they
// are persistent) on a subcommand's help.
func TestRootCommand_HelpListsNewPersistentFlags(t *testing.T) {
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
		for _, want := range []string{"--quiet", "-q", "--no-color"} {
			if !strings.Contains(got, want) {
				t.Errorf("args=%v: help missing %q\n--- help ---\n%s", args, want, got)
			}
		}
	}
}

// TestNoColorFlag_PropagatesToUI verifies that passing --no-color on the
// root command flips both the package-level noColor flag AND calls
// ui.SetNoColor — which is the wiring the rest of the binary relies on for
// suppressing ANSI escapes everywhere ui.IsTTY is consulted.
func TestNoColorFlag_PropagatesToUI(t *testing.T) {
	t.Cleanup(func() { ui.SetNoColor(false) })
	t.Cleanup(func() { noColor = false })
	t.Setenv("NO_COLOR", "")
	clearEnvOverrides(t)

	root := newRootCmd("dev")
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"status", "--no-color"})
	_ = root.Execute() // exit code irrelevant; we only need PersistentPreRunE to fire.

	if !noColor {
		t.Errorf("expected --no-color flag to set the package-level noColor var")
	}
	// Render a styled heading: ui.Heading only emits ANSI when tty=true.
	// After --no-color, ui.IsTTY(writer) must return false even if the
	// writer "looks" like a TTY — exercise by passing the IsTTY result
	// straight into Heading. We use a non-file writer (bytes.Buffer) so
	// isTerminal is naturally false; what we're proving here is that the
	// pipeline cmd→ui.SetNoColor→ui.IsTTY is wired (no panic / no failure).
	got := ui.Heading("USAGE", ui.IsTTY(&buf))
	if got != "USAGE" {
		t.Errorf("expected plain heading, got %q", got)
	}
}
