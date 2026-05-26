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

// apiKeyEnv is the environment variable that supplies a non-interactive API
// key. Honored when --api-key isn't passed; documented in the README.
const apiKeyEnv = "EMAILABLE_API_KEY"

// debugEnv is the environment variable that enables HTTP debug output, mirroring
// the --debug flag. Any non-empty value turns it on.
const debugEnv = "EMAILABLE_DEBUG"

// cmdCtx is the shared bag of state every command needs: active environment,
// loaded files, persistent flags. Commands grab one via newCmdCtx() in their
// RunE.
//
// File layout:
//
//   - Credentials / CredentialsPath: the global credentials file (env-suffixed
//     by the resolved env name). Login writes here, logout clears here, OAuth
//     refresh saves here. Holds api_key and the OAuth token bundle.
//   - GlobalConfigPath / ProjectConfigPath: the two scopes of the non-secret
//     config file. URLs only; both are user-managed (the CLI never writes
//     them). Surfaced for `emailable status` and diagnostics.
//
// Per-project API keys are deliberately NOT a file: use EMAILABLE_API_KEY
// instead.
//
// JSONMode and Quiet are populated from the persistent flag state at the
// time the cmdCtx is built. Commands should prefer reading these fields
// over the package-level globals so behavior remains consistent even when a
// command-local helper (e.g. applyStreamImplications) overrides the effective
// value for its caller.
type cmdCtx struct {
	Env             *env.Environment
	CredentialsPath string
	Credentials     *credentials.Credentials

	GlobalConfigPath  string
	ProjectConfigPath string

	JSONMode bool
	Quiet    bool

	// refreshNoticeWriter, when non-nil, receives a short stderr message the
	// first time requireAuth performs an OAuth refresh during this command's
	// lifetime. nil disables the notice (used in JSON mode).
	refreshNoticeWriter io.Writer
}

// newCmdCtxFor is the preferred constructor for command RunE bodies: it
// builds a cmdCtx and pre-wires the refresh-notice writer to the command's
// stderr. Commands that want the auto-refresh notice should use this rather
// than newCmdCtx.
//
// jsonMode is taken as a parameter (rather than read off the package-level
// jsonOutput global) so callers that compute an effective JSON value — e.g.
// applyStreamImplications — can pass the post-implication value without
// mutating the global.
func newCmdCtxFor(cmd *cobra.Command, jsonMode bool) (*cmdCtx, error) {
	c, err := newCmdCtx(jsonMode)
	if err != nil {
		return nil, err
	}
	return c.withRefreshNotice(cmd.ErrOrStderr()), nil
}

// newCmdCtx resolves the active environment, locates the credentials and
// config files, and loads the credentials (config files are inspected by
// env.Current and don't need to be retained on the context). Does not
// enforce that the user is logged in — that's the per-command's job via
// requireAuth.
//
// Quiet is read off the package-level flag global at call time (cobra has
// already populated it by the time any RunE fires) so callers only need to
// thread the JSON value through.
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

// apiKeySource is a label describing where the API key came from. Used by
// the status command (and surfaced as auth_source in JSON output).
//
// There's no flag-source variant on purpose: --api-key only exists on the
// `login` subcommand, where it triggers a save rather than a per-call
// override. By the time any other command runs, the key has either been
// promoted to the stored credentials or it isn't going to be used.
type apiKeySource string

const (
	apiKeySourceEnv     apiKeySource = "api-key (env)"
	apiKeySourceStored  apiKeySource = "api-key (stored)"
	apiKeySourceNone    apiKeySource = ""
	apiKeySourceOAuth   apiKeySource = "oauth"
	apiKeySourceMissing apiKeySource = "none"
)

// effectiveAPIKey returns the API key the CLI will use for the next
// request, along with a label describing its source. Resolution order:
// EMAILABLE_API_KEY env, then the stored Credentials.APIKey. Empty key +
// apiKeySourceNone when no key is configured.
func (c *cmdCtx) effectiveAPIKey() (string, apiKeySource) {
	if v := os.Getenv(apiKeyEnv); v != "" {
		return v, apiKeySourceEnv
	}
	if c.Credentials.APIKey != "" {
		return c.Credentials.APIKey, apiKeySourceStored
	}
	return "", apiKeySourceNone
}

// debugEnabled reports whether HTTP debug logging is on for this invocation.
// True when --debug was passed or EMAILABLE_DEBUG is set to any non-empty
// value.
func debugEnabled() bool {
	return debugMode || os.Getenv(debugEnv) != ""
}

// withRefreshNotice configures the context to emit a one-line stderr notice
// the first time it performs an OAuth refresh. Suppressed when JSONMode is
// true so machine-readable output stays clean.
func (c *cmdCtx) withRefreshNotice(w io.Writer) *cmdCtx {
	if !c.JSONMode {
		c.refreshNoticeWriter = w
	}
	return c
}

// requireAuth returns an *api.Client configured for the active environment.
// Resolution order:
//  1. EMAILABLE_API_KEY / stored API key — non-interactive auth; no refresh
//     path.
//  2. Stored OAuth access token — refreshed transparently when close to
//     expiry. A failed refresh caused by a permanently-dead refresh token
//     (oauth.ErrInvalidGrant) collapses to errNotAuthenticated so the user
//     is prompted to log in again; other failures propagate verbatim.
//  3. errNotAuthenticated — the user must `emailable login` or set an
//     API key.
func (c *cmdCtx) requireAuth() (*api.Client, error) {
	if key, _ := c.effectiveAPIKey(); key != "" {
		return api.NewWithOptions(c.Env.APIBaseURL, key, c.clientOptions()), nil
	}
	if c.Credentials.AccessToken == "" {
		return nil, errNotAuthenticated
	}
	if c.needsRefresh() {
		if err := c.refresh(context.Background()); err != nil {
			if errors.Is(err, oauth.ErrInvalidGrant) {
				return nil, errNotAuthenticated
			}
			return nil, err
		}
	}
	return api.NewWithOptions(c.Env.APIBaseURL, c.Credentials.AccessToken, c.clientOptions()), nil
}

// clientOptions returns the api.Options used by requireAuth — currently
// just toggles debug logging when --debug or EMAILABLE_DEBUG is on.
func (c *cmdCtx) clientOptions() api.Options {
	return api.Options{Debug: debugEnabled()}
}

// needsRefresh reports whether the stored access token is expired or close
// enough to expiry that we should refresh before the next request. Returns
// false when ExpiresAt is unset (older credentials without expiry tracking)
// so we don't refresh-loop on tokens that have no known TTL.
func (c *cmdCtx) needsRefresh() bool {
	if c.Credentials.RefreshToken == "" || c.Credentials.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().Add(refreshSkew).After(c.Credentials.ExpiresAt)
}

// refresh exchanges the stored refresh_token for a fresh access_token,
// updates c.Credentials in place, and persists to disk. When
// refreshNoticeWriter is non-nil, prints a short dimmed line so an attentive
// user sees that a refresh happened during their command.
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
