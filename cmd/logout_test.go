package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/emailable/emailable-cli/internal/config"
)

// TestLogout_RemovesConfig writes a fake config (with no access token so we
// don't hit the network attempting to revoke it), then runs `logout` and
// verifies the config file is gone and the success message was printed.
func TestLogout_RemovesConfig(t *testing.T) {
	// env.Current() walks up from the CWD looking for .emailable.yml,
	// and a developer's repo root may carry one for hitting a custom backend.
	// chdir to a tempdir so the test resolves the default ("default") env
	// — otherwise DefaultPath("default") and the path env.Current picks
	// disagree and the test removes the wrong file.
	t.Chdir(t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("EMAILABLE_API_URL", "")
	t.Setenv("EMAILABLE_OAUTH_URL", "")

	path, err := config.DefaultPath("default")
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}

	// No AccessToken: logout will skip Revoke, so this test stays offline.
	cfg := &config.Config{OwnerEmail: "user@example.com"}
	if err := cfg.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file should exist: %v", err)
	}

	root := newRootCmd("test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"logout"})

	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if !strings.Contains(out.String(), "Logged out.") {
		t.Errorf("expected output to contain 'Logged out.', got %q", out.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected config file to be removed, stat err = %v", err)
	}
}

// TestLogout_NoCredentials verifies logout is idempotent: with no config
// present it should still succeed and print "Logged out."
func TestLogout_NoCredentials(t *testing.T) {
	// See TestLogout_RemovesConfig — chdir away from the repo so the
	// project-local .emailable.yml isn't picked up by env.Current().
	t.Chdir(t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("EMAILABLE_API_URL", "")
	t.Setenv("EMAILABLE_OAUTH_URL", "")

	root := newRootCmd("test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"logout"})

	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "Logged out.") {
		t.Errorf("expected output to contain 'Logged out.', got %q", out.String())
	}
}
