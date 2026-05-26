package ui

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

func TestSpinner_NonTTY_PrintsOnceAndQuiet(t *testing.T) {
	var buf bytes.Buffer
	s := NewTo(&buf, "Waiting for auth")
	s.Start()
	time.Sleep(2 * TickInterval) // would have produced several frames on a TTY
	s.Stop()

	got := buf.String()
	if got != "Waiting for auth...\n" {
		t.Errorf("non-TTY output: got %q, want %q", got, "Waiting for auth...\n")
	}
}

func TestSpinner_StopWithoutStart_IsNoOp(t *testing.T) {
	var buf bytes.Buffer
	s := NewTo(&buf, "x")
	s.Stop() // must not panic / write
	if buf.Len() != 0 {
		t.Errorf("expected no output, got %q", buf.String())
	}
}

// TestIsTTY_NoColor verifies that NO_COLOR (https://no-color.org/) suppresses
// color even when the underlying writer would otherwise be detected as a TTY.
// isTerminal is swapped for a fake that always returns true so the test
// doesn't depend on a real PTY being attached during `go test`.
func TestIsTTY_NoColor(t *testing.T) {
	orig := isTerminal
	t.Cleanup(func() { isTerminal = orig })
	isTerminal = func(io.Writer) bool { return true }

	var buf bytes.Buffer

	// Baseline: with NO_COLOR unset, the fake "always TTY" check wins.
	t.Setenv("NO_COLOR", "")
	if !IsTTY(&buf) {
		t.Fatalf("baseline: IsTTY should be true when isTerminal=true and NO_COLOR is empty")
	}

	// NO_COLOR set to any non-empty value must disable color.
	t.Setenv("NO_COLOR", "1")
	if IsTTY(&buf) {
		t.Errorf("NO_COLOR=1: IsTTY should be false, got true")
	}

	// Spec says "any non-empty value" — try a non-numeric one too.
	t.Setenv("NO_COLOR", "yes")
	if IsTTY(&buf) {
		t.Errorf("NO_COLOR=yes: IsTTY should be false, got true")
	}

	// Sanity-check style helpers respect the propagated decision.
	if got := Cyan("x", IsTTY(&buf)); got != "x" {
		t.Errorf("Cyan with NO_COLOR set should return %q, got %q", "x", got)
	}
}

// TestSetNoColor verifies the --no-color "force off" path: with isTerminal
// stubbed to true, NO_COLOR unset, and noColorForce=true, IsTTY must report
// false. Toggling back to false restores the prior behavior.
func TestSetNoColor(t *testing.T) {
	orig := isTerminal
	t.Cleanup(func() { isTerminal = orig })
	isTerminal = func(io.Writer) bool { return true }
	t.Setenv("NO_COLOR", "")

	t.Cleanup(func() { SetNoColor(false) })

	var buf bytes.Buffer
	if !IsTTY(&buf) {
		t.Fatalf("baseline: IsTTY should be true with isTerminal=true, NO_COLOR unset, no force")
	}

	SetNoColor(true)
	if IsTTY(&buf) {
		t.Errorf("SetNoColor(true) should force IsTTY=false")
	}

	SetNoColor(false)
	if !IsTTY(&buf) {
		t.Errorf("SetNoColor(false) should restore IsTTY=true")
	}
}

func TestSpinner_SetMessage_NonTTY(t *testing.T) {
	var buf bytes.Buffer
	s := NewTo(&buf, "first")
	s.Start()
	s.SetMessage("second")
	s.Stop()
	// Non-TTY only prints the message at Start, so we should see "first".
	if !strings.Contains(buf.String(), "first") {
		t.Errorf("expected initial message in non-TTY output, got %q", buf.String())
	}
}
