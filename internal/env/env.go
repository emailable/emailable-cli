// Package env resolves the active runtime configuration: backend URLs and
// output format defaults.
//
// These live in a config.Config layered across three sources, in descending
// precedence:
//
//   - Environment variables (EMAILABLE_API_URL, EMAILABLE_OAUTH_URL,
//     EMAILABLE_OUTPUT).
//   - Project file at <project>/.emailable/config.json, discovered by
//     walking up from the current working directory.
//   - Global file at $XDG_CONFIG_HOME/emailable/config.json.
//
// All three use the same config.Config schema. Within a single source the
// API/OAuth URLs must be set together. Per-field, higher-precedence sources
// override lower-precedence ones.
package env

import (
	"fmt"
	"os"
	"strings"

	"github.com/emailable/emailable-cli/internal/config"
)

const (
	// PublicClientID is the OAuth client_id for the Emailable CLI. Public by
	// OAuth spec for a "public client" — embedded in every distributed binary
	// and registered with the same value on every Emailable environment.
	PublicClientID = "wdjuYuA3NZsKi-cR4mbaiBZ031iGt_a6zOpPQKzDFSI"

	DefaultAPIBaseURL   = "https://api.emailable.com/v1"
	DefaultOAuthBaseURL = "https://app.emailable.com"

	envAPIURL         = "EMAILABLE_API_URL"
	envOAuthURL       = "EMAILABLE_OAUTH_URL"
	envOutput         = "EMAILABLE_OUTPUT"
	envOptOutNotifier = "EMAILABLE_NO_UPDATE_NOTIFIER"
)

// Environment holds the active host configuration.
type Environment struct {
	// Name is "default" for production endpoints, "custom" when overridden via
	// env vars, project file, or global file. Used to suffix the credentials
	// file so tokens for different backends don't collide.
	Name         string
	APIBaseURL   string
	OAuthBaseURL string
	ClientID     string
}

// MergedConfig returns the config that results from layering, per field:
// env vars (highest) > project file > global file > zero value (lowest).
//
// Within a single source, the api_url/oauth_url pair must be both-set or
// both-empty — partial sources are a configuration error.
func MergedConfig() (*config.Config, error) {
	merged := &config.Config{}

	globalPath, err := config.DefaultPath()
	if err == nil {
		g, loadErr := config.Load(globalPath)
		if loadErr != nil {
			return nil, fmt.Errorf("env: load %s: %w", globalPath, loadErr)
		}
		if (g.APIURL == "") != (g.OAuthURL == "") {
			return nil, fmt.Errorf("env: %s: api_url and oauth_url must both be set", globalPath)
		}
		applyOver(merged, g)
	}

	if path, ok := findProjectConfigFromCWD(); ok {
		p, err := loadProjectConfig(path)
		if err != nil {
			return nil, fmt.Errorf("env: load %s: %w", path, err)
		}
		applyOver(merged, p)
	}

	envAPI := os.Getenv(envAPIURL)
	envOAuthV := os.Getenv(envOAuthURL)
	if envAPI != "" || envOAuthV != "" {
		if envAPI == "" || envOAuthV == "" {
			return nil, fmt.Errorf("emailable: %s and %s must both be set", envAPIURL, envOAuthURL)
		}
		merged.APIURL = envAPI
		merged.OAuthURL = envOAuthV
	}
	if v := os.Getenv(envOutput); v != "" {
		merged.Output = strings.ToLower(v)
	}

	return merged, nil
}

// applyOver overlays src onto dst — non-zero fields in src win.
func applyOver(dst, src *config.Config) {
	if src.APIURL != "" {
		dst.APIURL = src.APIURL
	}
	if src.OAuthURL != "" {
		dst.OAuthURL = src.OAuthURL
	}
	if src.Output != "" {
		dst.Output = src.Output
	}
}

// Current resolves the active environment from the merged config.
func Current() (*Environment, error) {
	merged, err := MergedConfig()
	if err != nil {
		return nil, err
	}
	if merged.APIURL != "" {
		return &Environment{
			Name:         "custom",
			APIBaseURL:   merged.APIURL,
			OAuthBaseURL: merged.OAuthURL,
			ClientID:     PublicClientID,
		}, nil
	}
	return &Environment{
		Name:         "default",
		APIBaseURL:   DefaultAPIBaseURL,
		OAuthBaseURL: DefaultOAuthBaseURL,
		ClientID:     PublicClientID,
	}, nil
}

// UpdateNotifierOptOut reports whether EMAILABLE_NO_UPDATE_NOTIFIER is set
// to a truthy value. Exposed separately from MergedConfig so callers can
// honor the env var even when config-file parsing fails — a corrupt config
// must not override the user's explicit opt-out.
func UpdateNotifierOptOut() bool {
	return isTruthy(os.Getenv(envOptOutNotifier))
}

// isTruthy returns true for "1", "true", "yes", "on" (case-insensitive).
func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
