package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/emailable/emailable-cli/internal/env"
	"github.com/spf13/cobra"
)

// cliArgs returns the raw command line. A var so tests can stand in for os.Args.
var cliArgs = func() []string { return os.Args[1:] }

// flagUsageError marks a flag parse failure as invalid input (exit 4). Parsing
// stops at the bad flag, so a later --json or --jq hasn't been bound yet; the
// raw args are scanned for them so the error still renders as JSON.
func flagUsageError(_ *cobra.Command, err error) error {
	if want, explicit := jsonRequested(cliArgs()); explicit {
		if want {
			jsonOutput = true
		}
	} else {
		applyConfiguredOutput()
	}
	return NewInvalidInput(err.Error())
}

// unknownSubcommand rejects positional args on a command that only groups
// subcommands, so `emailable bogus` and `emailable batch bogus` fail as
// invalid input instead of printing help (or exiting 1).
func unknownSubcommand(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	// Arg validation runs before PersistentPreRunE, which is where --jq and
	// the configured output mode normally take effect.
	if jqExpr != "" {
		jsonOutput = true
	} else if !cmd.Flags().Changed("json") {
		applyConfiguredOutput()
	}
	msg := fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2 // cobra's own default for this message
	}
	if suggestions := cmd.SuggestionsFor(args[0]); len(suggestions) > 0 {
		msg += fmt.Sprintf("; did you mean %q?", suggestions[0])
	}
	return NewInvalidInput(msg)
}

// showHelp is the RunE for grouping commands, so they stay runnable and
// unknownSubcommand gets a chance to reject stray args.
func showHelp(cmd *cobra.Command, _ []string) error {
	return cmd.Help()
}

func applyConfiguredOutput() {
	if merged, err := env.MergedConfig(); err == nil && strings.EqualFold(merged.Output, "json") {
		jsonOutput = true
	}
}

// jsonRequested reports whether args ask for JSON output via --json or --jq,
// and whether either flag appears at all.
func jsonRequested(args []string) (want, explicit bool) {
	for i, a := range args {
		if a == "--" {
			break
		}
		switch {
		case a == "--json":
			want, explicit = true, true
		case strings.HasPrefix(a, "--json="):
			v, err := strconv.ParseBool(strings.TrimPrefix(a, "--json="))
			want, explicit = err == nil && v, true
		case a == "--jq" && i+1 < len(args), strings.HasPrefix(a, "--jq="):
			return true, true
		}
	}
	return want, explicit
}
