package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/emailable/emailable-cli/internal/api"
	"github.com/emailable/emailable-cli/internal/credentials"
	"github.com/emailable/emailable-cli/internal/oauth"
	"github.com/emailable/emailable-cli/internal/output"
	"github.com/emailable/emailable-cli/internal/ui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// newLoginCmd returns the `emailable login` cobra command. The actual flow
// (OAuth device authorization, or API-key save) lives in runLoginE.
//
// `--api-key` is intentionally a local flag here rather than a persistent
// root flag — credentials don't belong on argv for every command, where
// they'd land in shell history and `ps` output. Login is the one place a
// user has explicitly opted into committing the key, so the flag lives
// here and nowhere else.
func newLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "login",
		Short:        "Log in to your Emailable account",
		Args:         wrapInvalidInputArgs(cobra.NoArgs),
		SilenceUsage: true,
		Example: `  # Interactive OAuth device login
  emailable login

  # Save an API key non-interactively
  emailable login --api-key sk_live_xxx

  # Pipe an API key from a secret manager
  op read "op://Personal/Emailable/api_key" | emailable login`,
		RunE: runLoginE,
	}
	cmd.Flags().StringVar(&apiKey, "api-key", "", "Save an API key as your credential (or pipe the key to stdin)")
	return cmd
}

// runLoginE drives one of two flows depending on how it's invoked:
//
//   - API key (non-interactive): if `--api-key VALUE` is set, or stdin is
//     piped (e.g. `op read ... | emailable login`), the key is persisted
//     to the credentials file and validated against /v1/account to fetch
//     the owner email. No OAuth round-trip happens.
//   - OAuth device flow (interactive): the default — request a device
//     code, open the browser to authorize, poll for the access_token,
//     persist credentials, fetch the owner email.
//
// oauth.ErrAccessDenied and oauth.ErrExpiredToken collapse to friendly
// messages; other errors propagate verbatim so the user sees the cause.
func runLoginE(cmd *cobra.Command, _ []string) error {
	ctx, err := newCmdCtx(jsonOutput)
	if err != nil {
		return err
	}

	// API-key path takes precedence over OAuth. We deliberately do NOT
	// consult EMAILABLE_API_KEY here — that env var is for per-invocation
	// use; logging in is an explicit persistence action and should require
	// the user to commit to it via flag or stdin pipe.
	if key, ok := apiKeyForLogin(); ok {
		return loginWithAPIKey(cmd, ctx, key)
	}

	client := oauth.NewClient(ctx.Env.OAuthBaseURL, ctx.Env.ClientID, nil)

	dc, err := client.RequestDeviceCode(cmd.Context())
	if err != nil {
		return err
	}

	hStderr := &output.Human{W: cmd.ErrOrStderr(), Quiet: ctx.Quiet}

	openURL := dc.VerificationURIComplete
	if openURL == "" {
		openURL = dc.VerificationURI
	}
	// verification_uri_complete embeds the code, so we open the browser
	// straightaway rather than gating on a keypress. We always print the code
	// and URL too — never just on failure: over SSH or in a container the open
	// may "succeed" on a browser the user can't see, and the URL is their only
	// way through. The code lets them confirm the page matches what we sent.
	if err := openBrowser(openURL); err != nil {
		_ = hStderr.Notice("Couldn't open your browser automatically.")
	} else {
		_ = hStderr.Notice("Opening your browser to authorize.")
	}
	_ = hStderr.Notice(fmt.Sprintf("Verification code: `%s`", dc.UserCode))
	_ = hStderr.Notice(fmt.Sprintf("If it doesn't open, visit `%s`", openURL))

	sp := ui.New("Waiting for authorization")
	sp.Start()
	tok, err := client.PollToken(cmd.Context(), dc)
	sp.Stop()
	if err != nil {
		if errors.Is(err, oauth.ErrAccessDenied) {
			return errors.New("authorization was denied")
		}
		if errors.Is(err, oauth.ErrExpiredToken) {
			return errors.New("device code expired before authorization completed")
		}
		return fmt.Errorf("login: %w", err)
	}

	creds := &credentials.Credentials{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
	}
	if tok.ExpiresIn > 0 {
		creds.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	if err := creds.Save(ctx.CredentialsPath); err != nil {
		return err
	}

	// Best-effort fetch of the owner email so the success line is
	// personalized. A failure here doesn't undo the login — the token is
	// already on disk above — we just fall back to a generic message.
	apiClient := api.New(ctx.Env.APIBaseURL, creds.AccessToken, nil)
	acc, accErr := apiClient.Account(cmd.Context())
	h := &output.Human{W: cmd.OutOrStdout(), Quiet: ctx.Quiet}
	if accErr == nil && acc != nil {
		creds.OwnerEmail = acc.OwnerEmail
		if saveErr := creds.Save(ctx.CredentialsPath); saveErr != nil {
			// Login succeeded; the second save (adding owner_email) is a
			// best-effort enhancement. Surface a dimmed note so the user
			// knows their credentials file is slightly incomplete without
			// aborting the login flow.
			noticeW := &output.Human{W: cmd.ErrOrStderr(), Quiet: ctx.Quiet}
			_ = noticeW.Notice(fmt.Sprintf("Couldn't update owner_email in credentials: %v", saveErr))
		}
		return h.Success(fmt.Sprintf("Logged in as %s", acc.OwnerEmail))
	}
	return h.Success("Logged in.")
}

// apiKeyForLogin returns the API key the user supplied for this login
// invocation and a bool indicating whether one was provided. Two sources,
// in order:
//
//  1. The persistent --api-key flag (e.g. `emailable login --api-key XXX`).
//     Convenient but the key lands in shell history.
//  2. Piped stdin (e.g. `op read ... | emailable login`). Preferred for
//     secrets because nothing about the key is recorded by the shell.
//
// EMAILABLE_API_KEY is intentionally NOT consulted here — see runLoginE.
func apiKeyForLogin() (string, bool) {
	if apiKey != "" {
		return strings.TrimSpace(apiKey), true
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", false
		}
		key := strings.TrimSpace(string(data))
		if key != "" {
			return key, true
		}
	}
	return "", false
}

// loginWithAPIKey persists key to the credentials file, then calls
// /v1/account to validate it and grab the owner email. A bad key surfaces
// as whatever /v1/account returns (typically a 401 with
// code=not_authenticated) — we don't write the key to disk until we know
// it works.
func loginWithAPIKey(cmd *cobra.Command, ctx *cmdCtx, key string) error {
	// Validate first against /v1/account so a typo doesn't silently leave
	// a broken key on disk.
	apiClient := api.NewWithOptions(ctx.Env.APIBaseURL, key, api.Options{Debug: debugEnabled()})
	acc, err := apiClient.Account(cmd.Context())
	if err != nil {
		return err
	}

	creds := ctx.Credentials
	creds.APIKey = key
	// Saving a fresh API key supersedes any prior OAuth credentials; clear
	// them so the auth source is unambiguous and so a later `logout`
	// doesn't try to revoke a token the user has already abandoned.
	creds.AccessToken = ""
	creds.RefreshToken = ""
	creds.ExpiresAt = time.Time{}
	if acc != nil {
		creds.OwnerEmail = acc.OwnerEmail
	}
	if err := creds.Save(ctx.CredentialsPath); err != nil {
		return err
	}

	h := &output.Human{W: cmd.OutOrStdout(), Quiet: ctx.Quiet}
	if acc != nil && acc.OwnerEmail != "" {
		return h.Success(fmt.Sprintf("Logged in as %s (API key)", acc.OwnerEmail))
	}
	return h.Success("Logged in with API key.")
}

// openBrowser launches the OS's default browser pointing at url. Returns an
// error when the platform isn't supported or the launch command fails to
// start. The child process is detached — we don't wait on it.
func openBrowser(url string) error {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "linux", "freebsd", "openbsd", "netbsd":
		c = exec.Command("xdg-open", url)
	case "windows":
		// Empty title arg is required so URLs containing & aren't parsed as
		// the window title by cmd's start builtin.
		c = exec.Command("cmd", "/c", "start", "", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	return c.Start()
}
