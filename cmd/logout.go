package cmd

import (
	"github.com/emailable/emailable-cli/internal/config"
	"github.com/emailable/emailable-cli/internal/oauth"
	"github.com/emailable/emailable-cli/internal/output"
	"github.com/spf13/cobra"
)

// newLogoutCmd returns the `emailable logout` cobra command. The actual
// flow (revoke + clear stored credentials) lives in runLogoutE.
func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "logout",
		Short:        "Log out and remove stored credentials",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example: `  # Log out and revoke the stored OAuth token
  emailable logout`,
		RunE: runLogoutE,
	}
}

// runLogoutE clears any stored credentials — OAuth access token AND/OR
// stored API key. For OAuth, best-effort revokes the token at the server
// first (errors ignored — server may be down or the token already
// invalid). API keys aren't revocable client-side, so the cleanup is just
// deleting the config file. Idempotent: running logout when not logged in
// still succeeds.
func runLogoutE(cmd *cobra.Command, _ []string) error {
	ctx, err := newCmdCtx(jsonOutput)
	if err != nil {
		return err
	}

	if ctx.Config.AccessToken != "" {
		client := oauth.NewClient(ctx.Env.OAuthBaseURL, ctx.Env.ClientID, nil)
		// Best-effort: server may be down, or token already invalidated.
		_ = client.Revoke(cmd.Context(), ctx.Config.AccessToken)
	}

	if err := config.Clear(ctx.ConfigPath); err != nil {
		return err
	}

	if jsonOutput {
		return (&output.JSON{W: cmd.OutOrStdout()}).Print(map[string]any{
			"logged_out": true,
			"message":    "Logged out.",
		})
	}

	h := &output.Human{W: cmd.OutOrStdout(), Quiet: ctx.Quiet}
	return h.Success("Logged out.")
}
