package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/emailable/emailable-cli/internal/updater"
)

// TestWaitAndNotify_AbandonsSlowCheck asserts the post-command grace window
// is bounded: a hung update-check goroutine cannot delay process exit by
// more than updateNoticeWait. Critical correctness property — without this
// a flaky GitHub could noticeably slow every CLI invocation.
func TestWaitAndNotify_AbandonsSlowCheck(t *testing.T) {
	var buf bytes.Buffer
	resultCh := make(chan updater.Result) // never sent on
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	start := time.Now()
	waitAndNotify(&buf, resultCh, cancel, 50*time.Millisecond)
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Errorf("waitAndNotify blocked %v, expected ~50ms", elapsed)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output on timeout, got %q", buf.String())
	}
}

// TestWaitAndNotify_PrintsAvailableUpdate verifies the notice is written
// when the check finishes within the grace window with an actionable result.
func TestWaitAndNotify_PrintsAvailableUpdate(t *testing.T) {
	var buf bytes.Buffer
	resultCh := make(chan updater.Result, 1)
	resultCh <- updater.Result{
		CurrentVersion:  "0.1.0",
		LatestVersion:   "0.2.0",
		UpdateAvailable: true,
	}
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	waitAndNotify(&buf, resultCh, cancel, time.Second)

	if !strings.Contains(buf.String(), "0.1.0 → 0.2.0") {
		t.Errorf("expected update notice in output, got %q", buf.String())
	}
}

// TestWaitAndNotify_SilentWhenNoUpdate ensures a completed check with no
// update produces no output (zero-spam for happy-path users).
func TestWaitAndNotify_SilentWhenNoUpdate(t *testing.T) {
	var buf bytes.Buffer
	resultCh := make(chan updater.Result, 1)
	resultCh <- updater.Result{} // zero -> no notice
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	waitAndNotify(&buf, resultCh, cancel, time.Second)
	if buf.Len() != 0 {
		t.Errorf("expected no output when result is zero, got %q", buf.String())
	}
}

// TestShouldSkip_JSONModeFromRootCmd verifies that running the root cobra
// command with --json sets jsonOutput, which would make ShouldSkip return
// SkipJSON. This is the seam our Execute() relies on to suppress the notice
// for machine-readable invocations.
func TestShouldSkip_JSONModeFromRootCmd(t *testing.T) {
	prev := jsonOutput
	t.Cleanup(func() { jsonOutput = prev })

	cmd := newRootCmd("0.1.0")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--json", "version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !jsonOutput {
		t.Fatal("expected jsonOutput=true after --json")
	}
	skip := updater.ShouldSkip(updater.Conditions{
		CurrentVersion: "0.1.0",
		JSONMode:       jsonOutput,
		StderrTTY:      true,
		Env:            func(string) string { return "" },
	})
	if skip != updater.SkipJSON {
		t.Errorf("ShouldSkip = %v, want SkipJSON", skip)
	}
}
