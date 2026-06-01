package cmd

import (
	"github.com/emailable/emailable-cli/internal/output"
	"github.com/spf13/cobra"
)

func newAccountCmd() *cobra.Command {
	account := &cobra.Command{
		Use:          "account",
		Short:        "Manage your Emailable account",
		SilenceUsage: true,
		Example: `  # Show the owner email and remaining credits
  emailable account status`,
	}

	status := &cobra.Command{
		Use:          "status",
		Short:        "Show owner email and remaining credits",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example: `  # Human-readable summary
  emailable account status

  # JSON for scripts
  emailable account status --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cctx, err := newCmdCtxFor(cmd, jsonOutput)
			if err != nil {
				return err
			}
			client, err := cctx.requireAuth(cmd.Context())
			if err != nil {
				return err
			}
			a, err := client.Account(cmd.Context())
			if err != nil {
				return err
			}
			view := &output.AccountView{
				OwnerEmail:       a.OwnerEmail,
				AvailableCredits: a.AvailableCredits,
			}
			return newOutput(cmd.OutOrStdout(), jsonOutput).Print(view)
		},
	}

	account.AddCommand(status)
	return account
}
