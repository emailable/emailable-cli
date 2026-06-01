package cmd

import (
	"github.com/emailable/emailable-cli/internal/credentials"
	"github.com/emailable/emailable-cli/internal/oauth"
	"github.com/emailable/emailable-cli/internal/output"
	"github.com/spf13/cobra"
)

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

func runLogoutE(cmd *cobra.Command, _ []string) error {
	ctx, err := newCmdCtx(jsonOutput)
	if err != nil {
		return err
	}

	if ctx.Credentials.AccessToken != "" {
		client := oauth.NewClient(ctx.Env.OAuthBaseURL, ctx.Env.ClientID, nil)
		// Best-effort: server may be down, or token already invalidated.
		_ = client.Revoke(cmd.Context(), ctx.Credentials.AccessToken)
	}

	if err := credentials.Clear(ctx.CredentialsPath); err != nil {
		return err
	}

	if jsonOutput {
		return newJSON(cmd.OutOrStdout()).Print(map[string]any{
			"logged_out": true,
			"message":    "Logged out.",
		})
	}

	h := &output.Human{W: cmd.OutOrStdout(), Quiet: ctx.Quiet}
	return h.Success("Logged out.")
}
