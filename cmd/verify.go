package cmd

import (
	"github.com/emailable/emailable-cli/internal/api"
	"github.com/emailable/emailable-cli/internal/ui"
	"github.com/spf13/cobra"
)

func newVerifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify EMAIL",
		Short: "Verify a single email in real time",
		Long:  "Verify a single email in real time. For multiple emails or a file, use `emailable batch verify`.",
		Example: `  # Verify a single email
  emailable verify hello@example.com

  # JSON output for scripts
  emailable verify hello@example.com --json`,
		Args:         wrapInvalidInputArgs(cobra.ExactArgs(1)),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			email := args[0]
			if !looksLikeEmail(email) {
				if looksLikeBatchInput(email) {
					return NewInvalidInputf("verify takes a single email address. For a file or list, use 'emailable batch verify %s'", email)
				}
				return NewInvalidInputf("%q is not a valid email address", email)
			}

			ctx, err := newCmdCtxFor(cmd, jsonOutput)
			if err != nil {
				return err
			}
			client, err := ctx.requireAuth(cmd.Context())
			if err != nil {
				return err
			}

			opts, err := verifyOptionsFromFlags(cmd)
			if err != nil {
				return err
			}

			f := newOutput(cmd.OutOrStdout(), jsonOutput)

			var sp *ui.Spinner
			if !jsonOutput && !ctx.Quiet {
				sp = ui.NewTo(cmd.ErrOrStderr(), "Verifying "+email)
				sp.Start()
			}
			result, err := client.Verify(cmd.Context(), email, opts)
			if sp != nil {
				sp.Stop()
			}
			if err != nil {
				return err
			}
			return f.Print(result)
		},
	}
	cmd.Flags().Bool("smtp", true, "Perform the SMTP step (disabling speeds responses but reduces accuracy)")
	cmd.Flags().Bool("accept-all", false, "Perform an Accept-All check (heavily impacts response time)")
	cmd.Flags().Int("timeout", 0, "Timeout to wait for response, in seconds (2–10)")
	return cmd
}

// verifyOptionsFromFlags only populates opts for flags the user explicitly set,
// so omitted flags fall through to server defaults rather than cobra's zero values.
func verifyOptionsFromFlags(cmd *cobra.Command) (*api.VerifyOptions, error) {
	opts := &api.VerifyOptions{}
	any := false
	if cmd.Flags().Changed("smtp") {
		v, err := cmd.Flags().GetBool("smtp")
		if err != nil {
			return nil, err
		}
		opts.SMTP = &v
		any = true
	}
	if cmd.Flags().Changed("accept-all") {
		v, err := cmd.Flags().GetBool("accept-all")
		if err != nil {
			return nil, err
		}
		opts.AcceptAll = &v
		any = true
	}
	if cmd.Flags().Changed("timeout") {
		v, err := cmd.Flags().GetInt("timeout")
		if err != nil {
			return nil, err
		}
		if v < 2 || v > 10 {
			return nil, NewInvalidInputf("--timeout must be between 2 and 10 seconds (got %d)", v)
		}
		opts.Timeout = v
		any = true
	}
	if !any {
		return nil, nil
	}
	return opts, nil
}
