// Package env resolves the active runtime configuration (backend URLs, output format) from env vars,
// project file, and global file in descending precedence.
package env

import (
	"fmt"
	"os"
	"strings"

	"github.com/emailable/emailable-cli/internal/config"
)

const (
	// PublicClientID is embedded in every binary; public by OAuth spec for a native/CLI client.
	PublicClientID = "wdjuYuA3NZsKi-cR4mbaiBZ031iGt_a6zOpPQKzDFSI"

	// DefaultAPIBaseURL is the production Emailable API base URL.
	DefaultAPIBaseURL = "https://api.emailable.com/v1"
	// DefaultOAuthBaseURL is the production Emailable OAuth base URL.
	DefaultOAuthBaseURL = "https://app.emailable.com"

	envAPIURL         = "EMAILABLE_API_URL"
	envOAuthURL       = "EMAILABLE_OAUTH_URL"
	envOutput         = "EMAILABLE_OUTPUT"
	envOptOutNotifier = "EMAILABLE_NO_UPDATE_NOTIFIER"
)

// Environment holds the resolved runtime configuration for a single backend.
type Environment struct {
	// Name suffixes the credentials file; "custom" when any URL is overridden so tokens don't collide.
	Name         string
	APIBaseURL   string
	OAuthBaseURL string
	ClientID     string
}

// MergedConfig returns the configuration merged from the global file, project file, and environment variables.
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

// Current returns the active Environment resolved from config files and environment variables.
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

// UpdateNotifierOptOut is separate from MergedConfig so a corrupt config file can't override an explicit opt-out.
func UpdateNotifierOptOut() bool {
	return isTruthy(os.Getenv(envOptOutNotifier))
}

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
