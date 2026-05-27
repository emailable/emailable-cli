package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/lipgloss"
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

	if err := runLoginE(cmd, nil); err != nil {
		return err
	}

	return printNextSteps(out)
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
