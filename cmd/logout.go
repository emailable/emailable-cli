package cmd

import (
	"os"

	"github.com/emailable/emailable-cli/internal/credentials"
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
		client := newOAuthClient(ctx.Env)
		// Best-effort: server may be down, or token already invalidated.
		_ = client.Revoke(cmd.Context(), ctx.Credentials.AccessToken)
	}

	if err := credentials.Clear(ctx.CredentialsPath); err != nil {
		return err
	}

	// A process can't unset its parent shell's variables, so an exported key
	// keeps authenticating after logout; say so, as `gh auth logout` does.
	envKey := os.Getenv(apiKeyEnv) != ""

	if jsonOutput {
		payload := map[string]any{
			"logged_out": true,
			"message":    "Logged out.",
		}
		if envKey {
			payload["api_key_env"] = true
		}
		return newJSON(cmd.OutOrStdout()).Print(payload)
	}

	h := &output.Human{W: cmd.OutOrStdout(), Quiet: ctx.Quiet}
	if err := h.Success("Logged out."); err != nil {
		return err
	}
	if envKey {
		return h.Hint("`EMAILABLE_API_KEY` is set, so commands still authenticate with it. Clear it from your environment to fully log out.")
	}
	return nil
}
