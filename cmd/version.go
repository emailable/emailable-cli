package cmd

import (
	"fmt"

	"github.com/emailable/emailable-cli/internal/env"
	"github.com/spf13/cobra"
)

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
		// dirty is only meaningful alongside a commit; omit both when there's no VCS info.
		payload["dirty"] = vi.Dirty
	}
	if e, err := env.Current(); err == nil && e.Name != "default" {
		payload["env"] = e.Name
	}

	return newJSON(cmd.OutOrStdout()).Print(payload)
}
