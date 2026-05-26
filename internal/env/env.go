// Package env resolves the API host, OAuth host, and OAuth client_id the CLI
// talks to. Defaults to production. Override at runtime by either:
//
//   - Setting both EMAILABLE_API_URL and EMAILABLE_OAUTH_URL env vars (e.g.
//     via direnv, CI, or an inline export), or
//   - Dropping a .emailable.yml in the project root (or any ancestor up to
//     the user's home directory) with api_url and oauth_url keys.
//
// Env vars take precedence over the project config file.
package env

import (
	"fmt"
	"os"
)

const (
	// PublicClientID is the OAuth client_id for the Emailable CLI. Public by
	// OAuth spec for a "public client" — embedded in every distributed binary
	// and registered with the same value on every Emailable environment.
	PublicClientID = "wdjuYuA3NZsKi-cR4mbaiBZ031iGt_a6zOpPQKzDFSI"

	DefaultAPIBaseURL   = "https://api.emailable.com/v1"
	DefaultOAuthBaseURL = "https://app.emailable.com"
)

// Environment holds the active host configuration.
type Environment struct {
	// Name is "default" for production endpoints, "custom" when overridden via
	// env vars. Used to suffix the credentials file so tokens for different
	// backends don't collide.
	Name         string
	APIBaseURL   string
	OAuthBaseURL string
	ClientID     string
}

// Current resolves the active environment.
//
// Resolution order (high → low precedence):
//  1. EMAILABLE_API_URL + EMAILABLE_OAUTH_URL env vars.
//  2. .emailable.yml discovered by walking up from the current working
//     directory to the user's home directory.
//  3. Built-in production defaults.
//
// Setting only one of the two URLs (in either source) is an error.
func Current() (*Environment, error) {
	api := os.Getenv("EMAILABLE_API_URL")
	oauth := os.Getenv("EMAILABLE_OAUTH_URL")

	// Env vars take precedence over the project config file. Only consult
	// the file when neither env var is set — partial env-var overrides
	// (one set, one not) should error rather than silently mix sources.
	if api == "" && oauth == "" {
		if path, ok := findProjectConfigFromCWD(); ok {
			a, o, err := loadProjectConfig(path)
			if err != nil {
				return nil, fmt.Errorf("env: load %s: %w", path, err)
			}
			api = a
			oauth = o
		}
	}

	if api == "" && oauth == "" {
		return &Environment{
			Name:         "default",
			APIBaseURL:   DefaultAPIBaseURL,
			OAuthBaseURL: DefaultOAuthBaseURL,
			ClientID:     PublicClientID,
		}, nil
	}
	if api == "" || oauth == "" {
		return nil, fmt.Errorf("emailable: api_url and oauth_url must both be set (via env vars or .emailable.yml)")
	}
	return &Environment{
		Name:         "custom",
		APIBaseURL:   api,
		OAuthBaseURL: oauth,
		ClientID:     PublicClientID,
	}, nil
}
