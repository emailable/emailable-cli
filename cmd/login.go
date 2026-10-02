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

  # Pipe an API key from a secret manager
  op read "op://Personal/Emailable/api_key" | emailable login --api-key -

  # Pass an API key directly (lands in shell history)
  emailable login --api-key live_xxx`,
		RunE: runLoginE,
	}
	cmd.Flags().StringVar(&apiKey, "api-key", "", "API `key` to save as your credential (\"-\" reads it from stdin)")
	return cmd
}

func runLoginE(cmd *cobra.Command, _ []string) error {
	ctx, err := newCmdCtx(jsonOutput)
	if err != nil {
		return err
	}

	// EMAILABLE_API_KEY is not consulted here: it's for per-invocation use,
	// and login is an explicit persistence action requiring --api-key.
	// Changed, not a non-empty value: `--api-key "$UNSET_VAR"` must fail as
	// invalid input rather than silently fall through to the browser flow.
	if cmd.Flags().Changed("api-key") {
		key, err := apiKeyForLogin(cmd.InOrStdin(), apiKey)
		if err != nil {
			return err
		}
		return loginWithAPIKey(cmd, ctx, key)
	}

	client := newOAuthClient(ctx.Env)

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

	// The spinner is human chrome; JSON and quiet callers don't get it.
	sp := ui.New("Waiting for authorization")
	if !jsonOutput && !ctx.Quiet {
		sp.Start()
	}
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
	apiClient := api.NewWithOptions(ctx.Env.APIBaseURL, creds.AccessToken, ctx.clientOptions())
	acc, accErr := apiClient.Account(cmd.Context())
	ownerEmail := ""
	if accErr == nil && acc != nil {
		ownerEmail = acc.OwnerEmail
		creds.OwnerEmail = acc.OwnerEmail
		if saveErr := creds.Save(ctx.CredentialsPath); saveErr != nil {
			_ = hStderr.Notice(fmt.Sprintf("Couldn't update owner_email in credentials: %v", saveErr))
		}
	}

	if jsonOutput {
		return printLoginJSON(cmd, apiKeySourceOAuth, ownerEmail)
	}
	h := &output.Human{W: cmd.OutOrStdout(), Quiet: ctx.Quiet}
	if ownerEmail != "" {
		return h.Success(fmt.Sprintf("Logged in as %s", ownerEmail))
	}
	return h.Success("Logged in.")
}

// stdinIsTerminal is overridable in tests, which can't easily fake a TTY.
var stdinIsTerminal = func(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// apiKeyForLogin resolves the --api-key flag value. Stdin is read only for
// an explicit `-`, so a pipe that never closes can't hang a plain `login`.
func apiKeyForLogin(stdin io.Reader, flag string) (string, error) {
	if flag != "-" {
		key := strings.TrimSpace(flag)
		if key == "" {
			return "", NewInvalidInput("--api-key is empty; pass a key, or `-` to read it from stdin")
		}
		return key, nil
	}
	if stdinIsTerminal(stdin) {
		return "", NewInvalidInput("`--api-key -` reads the key from stdin, but stdin is a terminal; pipe the key in")
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", fmt.Errorf("read API key from stdin: %w", err)
	}
	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", NewInvalidInput("`--api-key -` read an empty API key from stdin")
	}
	return key, nil
}

func printLoginJSON(cmd *cobra.Command, source apiKeySource, ownerEmail string) error {
	payload := map[string]any{
		"logged_in":   true,
		"auth_source": string(source),
	}
	if ownerEmail != "" {
		payload["owner_email"] = ownerEmail
	}
	return newJSON(cmd.OutOrStdout()).Print(payload)
}

func loginWithAPIKey(cmd *cobra.Command, ctx *cmdCtx, key string) error {
	// Validate before writing to disk so a typo doesn't silently leave a broken key.
	apiClient := api.NewWithOptions(ctx.Env.APIBaseURL, key, ctx.clientOptions())
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

	if jsonOutput {
		ownerEmail := ""
		if acc != nil {
			ownerEmail = acc.OwnerEmail
		}
		return printLoginJSON(cmd, apiKeySourceStored, ownerEmail)
	}
	h := &output.Human{W: cmd.OutOrStdout(), Quiet: ctx.Quiet}
	if acc != nil && acc.OwnerEmail != "" {
		return h.Success(fmt.Sprintf("Logged in as %s (API key)", acc.OwnerEmail))
	}
	return h.Success("Logged in with API key.")
}

// openBrowser is a var so tests can stub it instead of launching a browser.
var openBrowser = func(url string) error {
	c, err := browserCommand(runtime.GOOS, url)
	if err != nil {
		return err
	}
	return c.Start()
}

// browserCommand builds the command that opens url on goos, split out so
// tests can check every platform's arguments without launching anything.
func browserCommand(goos, url string) (*exec.Cmd, error) {
	switch goos {
	case "darwin":
		return exec.Command("open", url), nil
	case "linux", "freebsd", "openbsd", "netbsd":
		return exec.Command("xdg-open", url), nil
	case "windows":
		// Not `cmd /c start`: cmd.exe splits an unquoted & in the URL into a
		// second command. rundll32 receives the URL as a plain argument.
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url), nil
	default:
		return nil, fmt.Errorf("unsupported platform: %s", goos)
	}
}
