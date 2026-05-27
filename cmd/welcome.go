package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/emailable/emailable-cli/internal/api"
	"github.com/emailable/emailable-cli/internal/output"
	"github.com/emailable/emailable-cli/internal/ui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// runRootDefault handles a bare `emailable` (no subcommand): the getting-started
// flow on an interactive first run, otherwise help.
func runRootDefault(cmd *cobra.Command, args []string) error {
	// A positional that wasn't a known subcommand lands here; show help so the
	// user sees the command list rather than the welcome flow.
	if len(args) > 0 {
		return cmd.Help()
	}

	cctx, err := newCmdCtx(jsonOutput)
	if err != nil {
		return err
	}
	if isFirstRun(cctx, cmd) {
		return runGettingStarted(cmd)
	}
	return cmd.Help()
}

// isFirstRun reports whether a bare invocation should launch the getting-
// started flow: an interactive terminal (both ends), not already
// authenticated, and not in a machine-output / quiet mode. NO_COLOR is
// deliberately not consulted here — those users still get onboarded, just with
// a static, uncolored mark.
func isFirstRun(cctx *cmdCtx, cmd *cobra.Command) bool {
	if jsonOutput || quietMode {
		return false
	}
	if _, loggedIn := authSourceFor(cctx); loggedIn {
		return false
	}
	return terminalsInteractive(cmd)
}

// terminalsInteractive reports whether the command's stdout and the process's
// stdin are both terminals. A package var so tests can stub it without a PTY.
var terminalsInteractive = func(cmd *cobra.Command) bool {
	out, ok := cmd.OutOrStdout().(*os.File)
	if !ok || !term.IsTerminal(int(out.Fd())) {
		return false
	}
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// runGettingStarted is the first-run onboarding shown for a bare, logged-out
// invocation on a terminal.
func runGettingStarted(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()

	ui.AnimateBrand(out)

	stf := output.StylerFor(out)
	title := stf(lipgloss.NewStyle().Bold(true).Foreground(ui.BrandPurple))
	// Body copy uses the terminal's default foreground (white on dark themes)
	// rather than a dim gray, so the welcome reads as primary text.
	body := stf(lipgloss.NewStyle())

	fmt.Fprintln(out)
	fmt.Fprintln(out, title.Render("Welcome to Emailable"))
	fmt.Fprintln(out)
	fmt.Fprintln(out, body.Render("The command-line interface for Emailable."))
	fmt.Fprintln(out, body.Render("Let's get you logged in — this only takes a moment."))
	fmt.Fprintln(out)

	loggedIn, err := runOnboardingLogin(cmd, out)
	if err != nil {
		return err
	}
	// Don't dangle NEXT STEPS in front of someone who bailed out (esc) without
	// logging in — they have no credentials to run those commands with.
	if !loggedIn {
		return nil
	}

	return printNextSteps(out)
}

// loginMethod is the user's choice on the getting-started auth menu.
type loginMethod int

const (
	loginMethodOAuth loginMethod = iota
	loginMethodAPIKey
	loginMethodCanceled
)

// runOnboardingLogin presents the two ways to authenticate — browser sign-in or
// an API key — as an arrow-key menu, then runs the chosen flow. It reports
// whether the user actually logged in, so the caller can skip the post-login
// NEXT STEPS when they canceled out (esc). Without an interactive terminal there
// are no keystrokes to read, so it skips the menu and falls back to the OAuth
// device flow. The gate is terminal-ness, not ui.IsTTY — a NO_COLOR user on a
// real terminal still gets the menu (rendered uncolored), like the rest of
// onboarding.
func runOnboardingLogin(cmd *cobra.Command, out io.Writer) (loggedIn bool, err error) {
	if !terminalsInteractive(cmd) {
		return true, runLoginE(cmd, nil)
	}

	method, err := chooseLoginMethod(out)
	if err != nil {
		return false, err
	}
	switch method {
	case loginMethodOAuth:
		return true, runLoginE(cmd, nil)
	case loginMethodAPIKey:
		return loginWithPromptedAPIKey(cmd, out)
	default: // loginMethodCanceled
		return false, nil
	}
}

// chooseLoginMethod renders the sign-in/API-key menu and maps the selection to
// a loginMethod.
func chooseLoginMethod(out io.Writer) (loginMethod, error) {
	choices := []ui.Choice{
		{Label: "Sign in to your account", Hint: "Opens your browser to authorize"},
		{Label: "Enter an API key", Hint: "Paste a key from your dashboard"},
	}
	idx, ok, err := ui.Select(os.Stdin, out, "How would you like to log in?", choices)
	if err != nil {
		return loginMethodCanceled, err
	}
	if !ok {
		return loginMethodCanceled, nil
	}
	if idx == 1 {
		return loginMethodAPIKey, nil
	}
	return loginMethodOAuth, nil
}

// loginWithPromptedAPIKey reads an API key from the terminal — masked as
// bullets so a paste still shows visible feedback — then validates and persists
// it via the shared login path. A rejected or empty key re-prompts in place
// rather than dropping the user back to the shell, so a fat-fingered paste is
// recoverable. A canceled prompt (esc / ctrl-c) aborts onboarding quietly.
// Failures that aren't about the key itself (network, server errors) propagate
// so the user isn't trapped looping on an outage.
func loginWithPromptedAPIKey(cmd *cobra.Command, out io.Writer) (loggedIn bool, err error) {
	ctx, err := newCmdCtx(jsonOutput)
	if err != nil {
		return false, err
	}
	notice := &output.Human{W: cmd.ErrOrStderr()}

	for {
		key, ok, err := ui.Prompt(os.Stdin, out, "Paste your API key:", true)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil // canceled
		}
		if key == "" {
			_ = notice.Notice("Enter your API key, or press esc to cancel.")
			continue
		}

		err = loginWithAPIKey(cmd, ctx, key)
		if err == nil {
			return true, nil
		}
		// An auth-rejection status means the key was refused — recoverable, so
		// re-prompt. Everything else propagates: notably 402 (out of credits)
		// means the key is valid, and re-typing it would just loop.
		var apiErr *api.Error
		if errors.As(err, &apiErr) && keyRejected(apiErr.StatusCode) {
			msg := "That API key wasn't accepted."
			if apiErr.Message != "" {
				msg = fmt.Sprintf("That API key wasn't accepted: %s", apiErr.Message)
			}
			_ = notice.Notice(msg + " Try again, or press esc to cancel.")
			continue
		}
		return false, err
	}
}

// keyRejected reports whether an API status means the key itself was refused,
// so re-prompting for a different key can help. Other statuses — 402
// out-of-credits, 404, 429, 5xx — aren't fixed by a new key and propagate
// instead.
func keyRejected(status int) bool {
	switch status {
	case 400, 401, 403:
		return true
	default:
		return false
	}
}

// printNextSteps prints a few starter commands to try after onboarding.
func printNextSteps(w io.Writer) error {
	tty := ui.IsTTY(w)
	examples := [][2]string{
		{"emailable verify hello@example.com", "Verify a single address"},
		{"emailable batch verify emails.csv --wait", "Verify a CSV in bulk"},
		{"emailable account status", "Check your credit balance"},
	}
	pad := 0
	for _, e := range examples {
		if n := len(e[0]); n > pad {
			pad = n
		}
	}
	if _, err := fmt.Fprintf(w, "\n%s\n", ui.Heading("NEXT STEPS", tty)); err != nil {
		return err
	}
	for _, e := range examples {
		gap := pad - len(e[0]) + 2
		if _, err := fmt.Fprintf(w, "  %s%*s%s\n", ui.Cyan(e[0], tty), gap, "", ui.Dim(e[1], tty)); err != nil {
			return err
		}
	}
	return nil
}
