package cmd

import (
	"fmt"

	"github.com/emailable/emailable-cli/internal/env"
	"github.com/emailable/emailable-cli/internal/output"
	"github.com/spf13/cobra"
)

// newVersionCmd returns the `emailable version` subcommand. By default it
// prints the same multi-line human blurb as `emailable --version` (matches
// gh's parallel `gh version` / `gh --version`). When the persistent --json
// flag is set, it emits a machine-readable object instead — useful for AI
// agents and scripts that want to query the running binary's identity.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "version",
		Short:        "Print version",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if jsonOutput {
				return writeVersionJSON(cmd)
			}
			fmt.Fprintln(cmd.OutOrStdout(), versionDisplay())
			return nil
		},
	}
}

// writeVersionJSON emits the structured version payload to the command's
// stdout. Fields are omitted when empty so callers can distinguish "unknown"
// from a zero value (e.g. no VCS data => no "commit" key at all).
func writeVersionJSON(cmd *cobra.Command) error {
	vi := collectVersionInfo()

	payload := map[string]interface{}{
		"version": vi.Version,
	}
	if vi.BuildDate != "" {
		payload["build_date"] = vi.BuildDate
	}
	if vi.Commit != "" {
		payload["commit"] = vi.Commit
		// dirty is only meaningful alongside a commit; without VCS info we
		// can't honestly say whether the tree was modified.
		payload["dirty"] = vi.Dirty
	}
	if e, err := env.Current(); err == nil && e.Name != "default" {
		payload["env"] = e.Name
	}

	// Route through the shared JSON formatter so colorization, NO_COLOR
	// gating, and indentation stay consistent with the rest of --json.
	return (&output.JSON{W: cmd.OutOrStdout()}).Print(payload)
}
