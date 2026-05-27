package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/emailable/emailable-cli/internal/output"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

// newManCmd returns the hidden `emailable man` command. It generates a
// man(1) page for the root command and one for each subcommand into the
// target directory, suitable for installation under /usr/local/share/man
// or for bundling in a release tarball. Hidden from --help because it's a
// release/packaging tool rather than something a user runs interactively.
func newManCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "man --output DIR",
		Short:  "Generate man(1) pages",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, _ := cmd.Flags().GetString("output")
			if out == "" {
				return NewInvalidInput("--output DIR is required")
			}
			abs, err := filepath.Abs(out)
			if err != nil {
				return fmt.Errorf("resolve output dir: %w", err)
			}
			if err := os.MkdirAll(abs, 0o755); err != nil {
				return fmt.Errorf("create output dir: %w", err)
			}
			// Walk from the root command so every subcommand gets a page.
			header := &doc.GenManHeader{
				Title:   "EMAILABLE",
				Section: "1",
				Source:  "Emailable CLI",
				Manual:  "Emailable Manual",
			}
			if err := doc.GenManTree(cmd.Root(), header, abs); err != nil {
				return fmt.Errorf("generate man pages: %w", err)
			}
			h := &output.Human{W: cmd.ErrOrStderr(), Quiet: quietMode}
			return h.Success(fmt.Sprintf("Generated man pages in %s", abs))
		},
	}
	cmd.Flags().StringP("output", "o", "", "Directory to write man pages to")
	return cmd
}
