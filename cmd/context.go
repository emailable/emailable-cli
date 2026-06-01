package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/emailable/emailable-cli/internal/api"
	"github.com/emailable/emailable-cli/internal/config"
	"github.com/emailable/emailable-cli/internal/credentials"
	"github.com/emailable/emailable-cli/internal/env"
	"github.com/emailable/emailable-cli/internal/oauth"
	"github.com/emailable/emailable-cli/internal/output"
	"github.com/spf13/cobra"
)

// refreshSkew is the safety margin before ExpiresAt at which we proactively
// refresh the access token. Anything tighter risks the token expiring
// mid-request; anything looser refreshes too eagerly.
const refreshSkew = 60 * time.Second

const apiKeyEnv = "EMAILABLE_API_KEY"

const debugEnv = "EMAILABLE_DEBUG"

type cmdCtx struct {
	Env             *env.Environment
	CredentialsPath string
	Credentials     *credentials.Credentials

	GlobalConfigPath  string
	ProjectConfigPath string

	JSONMode bool
	Quiet    bool

	refreshNoticeWriter io.Writer // non-nil enables a stderr notice on OAuth refresh; nil in JSON mode
}

func newCmdCtxFor(cmd *cobra.Command, jsonMode bool) (*cmdCtx, error) {
	c, err := newCmdCtx(jsonMode)
	if err != nil {
		return nil, err
	}
	return c.withRefreshNotice(cmd.ErrOrStderr()), nil
}

func newCmdCtx(jsonMode bool) (*cmdCtx, error) {
	e, err := env.Current()
	if err != nil {
		return nil, err
	}
	credPath, err := credentials.DefaultPath(e.Name)
	if err != nil {
		return nil, fmt.Errorf("resolve credentials path: %w", err)
	}
	creds, err := credentials.Load(credPath)
	if err != nil {
		return nil, err
	}
	globalConfigPath, _ := config.DefaultPath()
	projectConfigPath, _ := env.ProjectConfigPath()
	return &cmdCtx{
		Env:               e,
		CredentialsPath:   credPath,
		Credentials:       creds,
		GlobalConfigPath:  globalConfigPath,
		ProjectConfigPath: projectConfigPath,
		JSONMode:          jsonMode,
		Quiet:             quietMode,
	}, nil
}

type apiKeySource string

const (
	apiKeySourceEnv     apiKeySource = "api-key (env)"
	apiKeySourceStored  apiKeySource = "api-key (stored)"
	apiKeySourceNone    apiKeySource = ""
	apiKeySourceOAuth   apiKeySource = "oauth"
	apiKeySourceMissing apiKeySource = "none"
)

func (c *cmdCtx) effectiveAPIKey() (string, apiKeySource) {
	if v := os.Getenv(apiKeyEnv); v != "" {
		return v, apiKeySourceEnv
	}
	if c.Credentials.APIKey != "" {
		return c.Credentials.APIKey, apiKeySourceStored
	}
	return "", apiKeySourceNone
}

func debugEnabled() bool {
	return debugMode || os.Getenv(debugEnv) != ""
}

func (c *cmdCtx) withRefreshNotice(w io.Writer) *cmdCtx {
	if !c.JSONMode {
		c.refreshNoticeWriter = w
	}
	return c
}

func (c *cmdCtx) requireAuth(ctx context.Context) (*api.Client, error) {
	if key, _ := c.effectiveAPIKey(); key != "" {
		return api.NewWithOptions(c.Env.APIBaseURL, key, c.clientOptions()), nil
	}
	if c.Credentials.AccessToken == "" {
		return nil, errNotAuthenticated
	}
	if c.needsRefresh() {
		if err := c.refresh(ctx); err != nil {
			if errors.Is(err, oauth.ErrInvalidGrant) {
				return nil, errNotAuthenticated
			}
			return nil, err
		}
	}
	return api.NewWithOptions(c.Env.APIBaseURL, c.Credentials.AccessToken, c.clientOptions()), nil
}

func (c *cmdCtx) clientOptions() api.Options {
	return api.Options{Debug: debugEnabled()}
}

func (c *cmdCtx) needsRefresh() bool {
	// No ExpiresAt (older credentials without expiry tracking) means no known
	// TTL — don't refresh-loop on it.
	if c.Credentials.RefreshToken == "" || c.Credentials.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().Add(refreshSkew).After(c.Credentials.ExpiresAt)
}

func (c *cmdCtx) refresh(ctx context.Context) error {
	oc := oauth.NewClient(c.Env.OAuthBaseURL, c.Env.ClientID, nil)
	tok, err := oc.Refresh(ctx, c.Credentials.RefreshToken)
	if err != nil {
		return err
	}
	c.Credentials.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		c.Credentials.RefreshToken = tok.RefreshToken
	}
	if tok.ExpiresIn > 0 {
		c.Credentials.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	if err := c.Credentials.Save(c.CredentialsPath); err != nil {
		return err
	}
	if c.refreshNoticeWriter != nil {
		h := &output.Human{W: c.refreshNoticeWriter, Quiet: c.Quiet}
		_ = h.Notice("Refreshed access token.")
	}
	return nil
}
