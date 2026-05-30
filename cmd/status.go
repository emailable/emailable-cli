package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/emailable/emailable-cli/internal/output"
	"github.com/spf13/cobra"
)

// newStatusCmd returns the `emailable status` command: local auth state, no
// network call.
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "status",
		Short:        "Show local auth state (no network call)",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example: `  # Show the active env, credentials path, and auth source
  emailable status

  # JSON output for scripts and agents
  emailable status --json`,
		RunE: runStatusE,
	}
}

// runStatusE prints the active environment, file paths, and stored credential
// state. Never hits the network. Human mode renders a labeled block; --json
// emits a flat object.
func runStatusE(cmd *cobra.Command, _ []string) error {
	cctx, err := newCmdCtx(jsonOutput)
	if err != nil {
		return err
	}

	source, loggedIn := authSourceFor(cctx)
	expiresAt := ""
	expiresIn := 0
	if source == "oauth" && !cctx.Credentials.ExpiresAt.IsZero() {
		expiresAt = cctx.Credentials.ExpiresAt.UTC().Format(time.RFC3339)
		if secs := int(time.Until(cctx.Credentials.ExpiresAt).Seconds()); secs > 0 {
			expiresIn = secs
		}
	}

	if jsonOutput {
		payload := map[string]any{
			"logged_in":        loggedIn,
			"env":              cctx.Env.Name,
			"api_url":          cctx.Env.APIBaseURL,
			"oauth_url":        cctx.Env.OAuthBaseURL,
			"credentials_path": cctx.CredentialsPath,
			"config_path":      cctx.GlobalConfigPath,
			"auth_source":      source,
		}
		if cctx.ProjectConfigPath != "" {
			payload["project_config_path"] = cctx.ProjectConfigPath
		}
		if source == "oauth" && cctx.Credentials.OwnerEmail != "" {
			payload["owner_email"] = cctx.Credentials.OwnerEmail
		}
		if expiresAt != "" {
			payload["expires_at"] = expiresAt
			payload["expires_in"] = expiresIn
		}
		return newJSON(cmd.OutOrStdout()).Print(payload)
	}

	return printStatusHuman(cmd, cctx, source, loggedIn, expiresAt, expiresIn)
}

// authSourceFor returns the credential source the CLI would use for the next
// request, distinguishing the API-key locations (env, stored). Returns "oauth"
// for a stored OAuth token and "none" when no credentials are configured.
func authSourceFor(cctx *cmdCtx) (source string, loggedIn bool) {
	if _, src := cctx.effectiveAPIKey(); src != apiKeySourceNone {
		return string(src), true
	}
	if cctx.Credentials.AccessToken != "" {
		return string(apiKeySourceOAuth), true
	}
	return string(apiKeySourceMissing), false
}

// printStatusHuman renders the status block for human consumption.
func printStatusHuman(cmd *cobra.Command, cctx *cmdCtx, source string, loggedIn bool, expiresAt string, expiresIn int) error {
	w := cmd.OutOrStdout()
	stf := output.StylerFor(w)
	label := stf(lipgloss.NewStyle().Foreground(lipgloss.Color("241")))
	value := stf(lipgloss.NewStyle().Bold(true))
	dim := stf(lipgloss.NewStyle().Foreground(lipgloss.Color("241")))

	stateText := "Not logged in"
	stateColor := lipgloss.Color("241")
	if loggedIn {
		stateText = "Logged in"
		stateColor = lipgloss.Color("42")
	}
	stateStyle := stf(lipgloss.NewStyle().Foreground(stateColor).Bold(true))

	rows := [][2]string{
		{"Status:", stateStyle.Render(stateText)},
		{"Source:", value.Render(source)},
	}
	if source == "oauth" && cctx.Credentials.OwnerEmail != "" {
		rows = append(rows, [2]string{"Account:", value.Render(cctx.Credentials.OwnerEmail)})
	}
	if expiresAt != "" {
		expiry := expiresAt
		if expiresIn > 0 {
			expiry += dim.Render(fmt.Sprintf(" (in %s)", humanizeSeconds(expiresIn)))
		} else {
			expiry += dim.Render(" (expired)")
		}
		rows = append(rows, [2]string{"Expires:", expiry})
	}
	rows = append(rows,
		[2]string{"Env:", value.Render(cctx.Env.Name)},
		[2]string{"API URL:", dim.Render(cctx.Env.APIBaseURL)},
		[2]string{"OAuth URL:", dim.Render(cctx.Env.OAuthBaseURL)},
		[2]string{"Credentials:", dim.Render(cctx.CredentialsPath)},
		[2]string{"Config:", dim.Render(cctx.GlobalConfigPath)},
	)
	if cctx.ProjectConfigPath != "" {
		rows = append(rows, [2]string{"Project config:", dim.Render(cctx.ProjectConfigPath)})
	}

	width := 0
	for _, r := range rows {
		if n := len(r[0]); n > width {
			width = n
		}
	}
	for _, r := range rows {
		pad := width - len(r[0]) + 2
		if _, err := fmt.Fprintf(w, "%s%s%s\n", label.Render(r[0]), strings.Repeat(" ", pad), r[1]); err != nil {
			return err
		}
	}
	if !loggedIn {
		h := &output.Human{W: w, Quiet: cctx.Quiet}
		return h.Hint("Run `emailable login` to log in, or set `EMAILABLE_API_KEY` for non-interactive use.")
	}
	return nil
}

// humanizeSeconds renders a duration as "Nd", "Nh", "Nm", or "Ns" using the
// largest unit that yields a non-zero integer. Used in status for the
// "expires in" hint; full precision lives in the expires_at timestamp.
func humanizeSeconds(s int) string {
	switch {
	case s >= 86400:
		return fmt.Sprintf("%dd", s/86400)
	case s >= 3600:
		return fmt.Sprintf("%dh", s/3600)
	case s >= 60:
		return fmt.Sprintf("%dm", s/60)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
