package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestVersionCmd_Human verifies the default (no --json) path matches what
// versionDisplay() produces for the human blurb.
func TestVersionCmd_Human(t *testing.T) {
	resetJSONFlag(t)

	cmd := newRootCmd("0.1.0-test")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := buf.String()
	if !strings.Contains(got, "emailable version 0.1.0-test") {
		t.Errorf("human output missing version prefix, got %q", got)
	}
	// Must NOT look like JSON.
	if strings.HasPrefix(strings.TrimSpace(got), "{") {
		t.Errorf("default path should not produce JSON, got %q", got)
	}
}

// TestVersionCmd_JSON verifies that `version --json` emits a valid JSON
// object with the expected fields. We don't assert on BuildDate / Commit /
// Dirty values because those depend on the build environment (ldflags, VCS
// info); we only assert that "version" is present and the payload parses.
func TestVersionCmd_JSON(t *testing.T) {
	resetJSONFlag(t)
	clearEnvOverrides(t)

	cmd := newRootCmd("0.1.0-test")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"version", "--json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	out := buf.Bytes()
	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, out)
	}

	gotVersion, ok := payload["version"].(string)
	if !ok || gotVersion != "0.1.0-test" {
		t.Errorf("version field: got %v, want %q", payload["version"], "0.1.0-test")
	}

	// env should be omitted on the default environment (no overrides set).
	if _, present := payload["env"]; present {
		t.Errorf("env should be omitted for default environment, payload=%v", payload)
	}

	// dirty must only appear alongside a commit (we don't know whether
	// either will be present in this run, but the invariant must hold).
	_, hasCommit := payload["commit"]
	_, hasDirty := payload["dirty"]
	if hasDirty && !hasCommit {
		t.Errorf("dirty without commit is meaningless, payload=%v", payload)
	}
}

// TestWriteVersionJSON_OmitsEmptyFields exercises the field-omission rules
// directly so we don't rely on the test binary's VCS state.
func TestWriteVersionJSON_OmitsEmptyFields(t *testing.T) {
	resetJSONFlag(t)
	clearEnvOverrides(t)
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = false })

	// Swap in a known versionInfo by overriding the package-level inputs.
	prevVersion, prevBuildDate := version, buildDate
	version = "9.9.9-fixture"
	buildDate = ""
	t.Cleanup(func() {
		version = prevVersion
		buildDate = prevBuildDate
	})

	cmd := newVersionCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, buf.String())
	}
	if payload["version"] != "9.9.9-fixture" {
		t.Errorf("version: got %v, want %q", payload["version"], "9.9.9-fixture")
	}
	if _, ok := payload["build_date"]; ok {
		t.Errorf("build_date should be omitted when empty, payload=%v", payload)
	}
	if _, ok := payload["env"]; ok {
		t.Errorf("env should be omitted on default env, payload=%v", payload)
	}
}

// TestVersionDisplay_ReleaseURL verifies the release link only appears for a
// published-tag version, not for snapshot / pseudo-version / dev builds.
func TestVersionDisplay_ReleaseURL(t *testing.T) {
	cases := []struct {
		name    string
		version string
		wantURL bool
	}{
		{"release", "0.3.0", true},
		{"snapshot", "0.3.0-next", false},
		{"pseudo", "0.3.1-0.20260601120000-abcdef123456", false},
		{"dev", "dev", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prev := version
			version = tc.version
			t.Cleanup(func() { version = prev })

			got := versionDisplay()
			hasURL := strings.Contains(got, releaseURLPrefix)
			if hasURL != tc.wantURL {
				t.Errorf("version %q: got URL=%v, want %v\noutput: %q", tc.version, hasURL, tc.wantURL, got)
			}
			if tc.wantURL && !strings.Contains(got, releaseURLPrefix+"v"+tc.version) {
				t.Errorf("version %q: expected URL with tag v%s, got %q", tc.version, tc.version, got)
			}
		})
	}
}

// resetJSONFlag ensures the package-level jsonOutput is clean for each test
// and restored afterward. The flag is global state shared with the cobra
// PersistentFlags binding, so tests that run after a --json invocation would
// otherwise inherit the stale value.
func resetJSONFlag(t *testing.T) {
	t.Helper()
	prev := jsonOutput
	jsonOutput = false
	t.Cleanup(func() { jsonOutput = prev })
}

// clearEnvOverrides forces env.Current() to resolve to "default" by zeroing
// the URL override env vars AND chdir-ing into a sibling-less temp directory
// for the test's lifetime. The chdir step is what suppresses discovery of
// the repo-root .emailable/config.json (which points at staging); without it the
// "env" key would always show up as "custom" when the suite runs from a
// checkout of this repo.
func clearEnvOverrides(t *testing.T) {
	t.Helper()
	t.Setenv("EMAILABLE_API_URL", "")
	t.Setenv("EMAILABLE_OAUTH_URL", "")
	t.Setenv("HOME", t.TempDir()) // stop project-config walk at a synthetic home
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}
