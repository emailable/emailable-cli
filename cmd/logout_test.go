package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/emailable/emailable-cli/internal/credentials"
)

// TestLogout_RemovesCredentials writes a fake credentials file (with no
// access token so we don't hit the network attempting to revoke it), then
// runs `logout` and verifies the file is gone and the success message was
// printed.
func TestLogout_RemovesCredentials(t *testing.T) {
	// env.Current() walks up from the CWD looking for .emailable/config.json,
	// and a developer's repo root may carry one for hitting a custom backend.
	// chdir to a tempdir so the test resolves the default ("default") env
	// — otherwise DefaultPath("default") and the path env.Current picks
	// disagree and the test removes the wrong file.
	t.Chdir(t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("EMAILABLE_API_URL", "")
	t.Setenv("EMAILABLE_OAUTH_URL", "")

	path, err := credentials.DefaultPath("default")
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}

	// No AccessToken: logout will skip Revoke, so this test stays offline.
	creds := &credentials.Credentials{OwnerEmail: "user@example.com"}
	if err := creds.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("credentials file should exist: %v", err)
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
		t.Errorf("expected credentials file to be removed, stat err = %v", err)
	}
}

// TestLogout_NoCredentials verifies logout is idempotent: with no config
// present it should still succeed and print "Logged out."
func TestLogout_NoCredentials(t *testing.T) {
	// See TestLogout_RemovesConfig — chdir away from the repo so the
	// project-local .emailable/config.json isn't picked up by env.Current().
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

// TestLogout_EnvKeyHint: an exported EMAILABLE_API_KEY outlives logout (a
// process can't unset its parent shell's variables), so logout says so.
func TestLogout_EnvKeyHint(t *testing.T) {
	cases := []struct {
		name   string
		envKey string
		args   []string
		want   string
		absent string
	}{
		{"human with env key", "sk_env", []string{"logout"}, "EMAILABLE_API_KEY is set, so commands still authenticate", ""},
		{"human without env key", "", []string{"logout"}, "Logged out.", "EMAILABLE_API_KEY"},
		{"json with env key", "sk_env", []string{"logout", "--json"}, `"api_key_env": true`, ""},
		{"json without env key", "", []string{"logout", "--json"}, `"logged_out": true`, "api_key_env"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("EMAILABLE_API_URL", "")
			t.Setenv("EMAILABLE_OAUTH_URL", "")
			t.Setenv("EMAILABLE_API_KEY", tc.envKey)

			root := newRootCmd("test")
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(tc.args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("expected %q in output, got %q", tc.want, out.String())
			}
			if tc.absent != "" && strings.Contains(out.String(), tc.absent) {
				t.Errorf("did not expect %q in output, got %q", tc.absent, out.String())
			}
		})
	}
}
