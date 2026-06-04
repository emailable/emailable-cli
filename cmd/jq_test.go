package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/emailable/emailable-cli/internal/output"
)

// resetJQ saves and clears the package-level --jq state around a test.
func resetJQ(t *testing.T) {
	t.Helper()
	prevExpr, prevQuery := jqExpr, jqQuery
	jqExpr, jqQuery = "", nil
	t.Cleanup(func() { jqExpr, jqQuery = prevExpr, prevQuery })
}

func TestJQ_ImpliesJSONAndFilters(t *testing.T) {
	resetJSONFlag(t)
	resetJQ(t)
	clearEnvOverrides(t)

	cmd := newRootCmd("0.1.0-test")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"version", "--jq", ".version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	if got != "0.1.0-test\n" {
		t.Errorf("got %q, want %q", got, "0.1.0-test\n")
	}
	if strings.Contains(got, "{") {
		t.Errorf("filtered string result should not be a JSON document, got %q", got)
	}
}

func TestJQ_NoStaleQueryAcrossRuns(t *testing.T) {
	resetJSONFlag(t)
	resetJQ(t)
	clearEnvOverrides(t)

	first := newRootCmd("0.1.0-test")
	var fb bytes.Buffer
	first.SetOut(&fb)
	first.SetErr(&fb)
	first.SetArgs([]string{"version", "--jq", ".version"})
	if err := first.Execute(); err != nil {
		t.Fatalf("first execute: %v", err)
	}

	second := newRootCmd("0.1.0-test")
	var sb bytes.Buffer
	second.SetOut(&sb)
	second.SetErr(&sb)
	second.SetArgs([]string{"version", "--json"})
	if err := second.Execute(); err != nil {
		t.Fatalf("second execute: %v", err)
	}
	// Second run had no --jq, so it must emit the full document, not a scalar.
	if !strings.Contains(sb.String(), "{") {
		t.Errorf("second run should emit a full JSON object, got %q", sb.String())
	}
}

func TestJQ_BadExpression(t *testing.T) {
	resetJSONFlag(t)
	resetJQ(t)
	clearEnvOverrides(t)

	cmd := newRootCmd("0.1.0-test")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"version", "--jq", ".["})

	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error for a malformed --jq expression")
	}
	// --jq implies --json even when the expression fails to compile, so the
	// error renders as JSON rather than a human line.
	if !jsonOutput {
		t.Error("a bad --jq expression should still leave JSON mode enabled")
	}
}

func mustCompile(t *testing.T, expr string) *output.Query {
	t.Helper()
	q, err := output.CompileQuery(expr)
	if err != nil {
		t.Fatalf("CompileQuery(%q): %v", expr, err)
	}
	return q
}
