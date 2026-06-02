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

func runLoginE(cmd *cobra.Command, _ []string) error {
	ctx, err := newCmdCtx(jsonOutput)
	if err != nil {
		return err
	}

	// EMAILABLE_API_KEY is not consulted here — it's for per-invocation use;
	// login is an explicit persistence action requiring flag or stdin pipe.
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
	// Always print code+URL even on success: over SSH the browser may open
	// somewhere the user can't see, and the code confirms the page matches.
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

	// Best-effort: token is already on disk, so an account fetch failure only
	// degrades the success message, it doesn't undo the login.
	apiClient := api.New(ctx.Env.APIBaseURL, creds.AccessToken, nil)
	acc, accErr := apiClient.Account(cmd.Context())
	h := &output.Human{W: cmd.OutOrStdout(), Quiet: ctx.Quiet}
	if accErr == nil && acc != nil {
		creds.OwnerEmail = acc.OwnerEmail
		if saveErr := creds.Save(ctx.CredentialsPath); saveErr != nil {
			noticeW := &output.Human{W: cmd.ErrOrStderr(), Quiet: ctx.Quiet}
			_ = noticeW.Notice(fmt.Sprintf("Couldn't update owner_email in credentials: %v", saveErr))
		}
		return h.Success(fmt.Sprintf("Logged in as %s", acc.OwnerEmail))
	}
	return h.Success("Logged in.")
}

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

func loginWithAPIKey(cmd *cobra.Command, ctx *cmdCtx, key string) error {
	// Validate before writing to disk so a typo doesn't silently leave a broken key.
	apiClient := api.NewWithOptions(ctx.Env.APIBaseURL, key, api.Options{Debug: debugEnabled()})
	acc, err := apiClient.Account(cmd.Context())
	if err != nil {
		return err
	}

	creds := ctx.Credentials
	creds.APIKey = key
	// Clear OAuth fields so auth source is unambiguous and logout won't try
	// to revoke an abandoned token.
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
