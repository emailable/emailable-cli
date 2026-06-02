package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/emailable/emailable-cli/internal/api"
	"github.com/emailable/emailable-cli/internal/output"
	"github.com/emailable/emailable-cli/internal/skill"
	"github.com/emailable/emailable-cli/internal/ui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func runRootDefault(cmd *cobra.Command, args []string) error {
	// Unknown subcommand lands here; show help rather than the welcome flow.
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

// isFirstRun reports whether a bare invocation should launch onboarding.
// NO_COLOR is not consulted — those users still get onboarded, just uncolored.
func isFirstRun(cctx *cmdCtx, cmd *cobra.Command) bool {
	if jsonOutput || quietMode {
		return false
	}
	if _, loggedIn := authSourceFor(cctx); loggedIn {
		return false
	}
	return terminalsInteractive(cmd)
}

// terminalsInteractive is a var so tests can stub it without a PTY.
var terminalsInteractive = func(cmd *cobra.Command) bool {
	out, ok := cmd.OutOrStdout().(*os.File)
	if !ok || !term.IsTerminal(int(out.Fd())) {
		return false
	}
	return term.IsTerminal(int(os.Stdin.Fd()))
}

func runGettingStarted(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()

	ui.AnimateBrand(out)

	stf := output.StylerFor(out)
	title := stf(lipgloss.NewStyle().Bold(true).Foreground(ui.BrandPurple))
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
	if !loggedIn {
		return nil
	}

	if err := maybeOfferSkillInstall(cmd, out); err != nil {
		return err
	}

	return printNextSteps(out)
}

type loginMethod int

const (
	loginMethodOAuth loginMethod = iota
	loginMethodAPIKey
	loginMethodCanceled
)

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
		// Re-prompt only for auth rejections; 402 (out of credits) means the key
		// is valid, so looping would not help.
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

func keyRejected(status int) bool {
	switch status {
	case 400, 401, 403:
		return true
	default:
		return false
	}
}

func maybeOfferSkillInstall(cmd *cobra.Command, out io.Writer) error {
	if !terminalsInteractive(cmd) {
		return nil
	}

	stf := output.StylerFor(out)
	body := stf(lipgloss.NewStyle())
	fmt.Fprintln(out)
	fmt.Fprintln(out, body.Render("Using an AI coding agent? We can install a skill so it knows"))
	fmt.Fprintln(out, body.Render("how to verify emails with this CLI."))
	fmt.Fprintln(out)

	choices := []ui.Choice{
		{Label: "Yes", Hint: "Installs for Claude Code, Codex, and OpenCode"},
		{Label: "No", Hint: "Skip — run `emailable skill install` later"},
	}
	idx, ok, err := ui.Select(os.Stdin, out, "Install the Emailable agent skill?", choices)
	if err != nil {
		return err
	}
	if !ok || idx != 0 {
		return nil
	}

	res, err := skill.InstallAll()
	if err != nil {
		_ = (&output.Human{W: cmd.ErrOrStderr()}).Notice(fmt.Sprintf("Couldn't install skill: %v", err))
		return nil
	}
	h := &output.Human{W: out}
	if err := h.Success(fmt.Sprintf("Skill installed to %s", res.SkillPath)); err != nil {
		return err
	}
	if len(res.Links) > 0 {
		printLinks(out, res.Links)
	}
	return nil
}

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
