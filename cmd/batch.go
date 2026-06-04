package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/emailable/emailable-cli/internal/api"
	"github.com/emailable/emailable-cli/internal/output"
	"github.com/emailable/emailable-cli/internal/ui"
	"github.com/spf13/cobra"
)

func newBatchCmd() *cobra.Command {
	batch := &cobra.Command{
		Use:          "batch",
		Short:        "Verify a batch of emails",
		SilenceUsage: true,
		Example: `  # Submit a batch and wait for completion
  emailable batch verify emails.csv --wait

  # Check status of an existing batch
  emailable batch get bch_123`,
	}

	get := &cobra.Command{
		Use:   "get BATCH_ID",
		Short: "Get the status of a batch verification job",
		Long: "Get the status of a batch verification job. Returns an " +
			"in-progress status while verifying and the per-email results " +
			"once complete. Use `--wait` to poll until completion, or " +
			"`--partial` to include partial results while still verifying " +
			"(batches ≤ 1,000 emails only).",
		Args:         wrapInvalidInputArgs(cobra.ExactArgs(1)),
		SilenceUsage: true,
		Example: `  # Get the latest status / results for a batch
  emailable batch get bch_123

  # Block until the batch completes
  emailable batch get bch_123 --wait

  # Save results to a file
  emailable batch get bch_123 -o results.csv`,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			wait, _ := cmd.Flags().GetBool("wait")
			partial, _ := cmd.Flags().GetBool("partial")
			outPath, _ := cmd.Flags().GetString("output")
			showAll, _ := cmd.Flags().GetBool("all")

			cctx, err := newCmdCtxFor(cmd, jsonOutput)
			if err != nil {
				return err
			}
			client, err := cctx.requireAuth(cmd.Context())
			if err != nil {
				return err
			}

			if wait {
				if partial {
					return fmt.Errorf("--wait and --partial can't be combined: --wait already polls until completion")
				}
				s, err := waitForCompletion(cmd.Context(), client, id, cctx.JSONMode || cctx.Quiet, cmd.ErrOrStderr())
				if err != nil {
					return err
				}
				return renderBatchOutcome(cmd, cctx, s, id, outPath, showAll)
			}

			s, err := client.Batch(cmd.Context(), id, partial)
			if err != nil {
				return err
			}
			return renderBatchOutcome(cmd, cctx, s, id, outPath, showAll)
		},
	}
	get.Flags().Bool("wait", false, "Poll until the batch completes")
	get.Flags().Bool("partial", false, "Include partial results while the batch is still verifying (batches ≤ 1,000 emails)")
	get.Flags().StringP("output", "o", "", "Write results to FILE (.csv or .json; format inferred from extension)")
	get.Flags().Bool("all", false, "Print the full results table inline instead of a summary")

	verify := &cobra.Command{
		Use:   "verify EMAIL_OR_FILE [EMAIL_OR_FILE...]",
		Short: "Verify a batch of emails",
		Long: "Verify a batch of emails. Accepts one or more emails or `.csv` / " +
			"`.json` / `.txt` files. Prints the batch ID; use `--wait` to poll " +
			"until complete.",
		Args:         wrapInvalidInputArgs(cobra.MinimumNArgs(1)),
		SilenceUsage: true,
		Example: `  # Verify a CSV file and block until results are ready
  emailable batch verify emails.csv --wait

  # Verify two literal emails
  emailable batch verify alice@example.com bob@example.com`,
		RunE: func(cmd *cobra.Command, args []string) error {
			field, _ := cmd.Flags().GetString("field")
			wait, _ := cmd.Flags().GetBool("wait")
			outPath, _ := cmd.Flags().GetString("output")
			showAll, _ := cmd.Flags().GetBool("all")

			cctx, err := newCmdCtxFor(cmd, jsonOutput)
			if err != nil {
				return err
			}
			client, err := cctx.requireAuth(cmd.Context())
			if err != nil {
				return err
			}

			emails, err := collectEmails(args, field)
			if err != nil {
				return err
			}

			submitOpts, err := submitBatchOptionsFromFlags(cmd)
			if err != nil {
				return err
			}

			f := newOutput(cmd.OutOrStdout(), cctx.JSONMode)

			submit, err := client.SubmitBatch(cmd.Context(), emails, submitOpts)
			if err != nil {
				return err
			}

			if wait {
				// Print before polling so ctrl-c mid-wait still leaves the id visible.
				if !cctx.JSONMode && !cctx.Quiet {
					printBatchID(cmd.ErrOrStderr(), submit.ID)
				}
				final, err := waitForCompletion(cmd.Context(), client, submit.ID, cctx.JSONMode || cctx.Quiet, cmd.ErrOrStderr())
				if err != nil {
					return err
				}
				return renderBatchOutcome(cmd, cctx, final, submit.ID, outPath, showAll)
			}

			return f.Print(submit)
		},
	}
	verify.Flags().String("field", "", "CSV column or JSON key `<name>` holding the email (defaults to email)")
	verify.Flags().Bool("wait", false, "Poll until the batch completes")
	verify.Flags().StringP("output", "o", "", "Write results to FILE (.csv or .json; format inferred from extension)")
	verify.Flags().Bool("all", false, "Print the full results table inline instead of a summary")
	verify.Flags().String("url", "", "URL that will receive the batch results via HTTP POST")
	verify.Flags().Bool("retries", true, "Retry verifications when mail servers return certain responses, increasing accuracy")
	verify.Flags().StringSlice("response-fields", nil, "Fields to include in the response (default: all)")

	batch.AddCommand(get, verify)
	return batch
}

func submitBatchOptionsFromFlags(cmd *cobra.Command) (*api.SubmitBatchOptions, error) {
	opts := &api.SubmitBatchOptions{}
	any := false
	if cmd.Flags().Changed("url") {
		v, err := cmd.Flags().GetString("url")
		if err != nil {
			return nil, err
		}
		opts.URL = v
		any = true
	}
	if cmd.Flags().Changed("retries") {
		v, err := cmd.Flags().GetBool("retries")
		if err != nil {
			return nil, err
		}
		opts.Retries = &v
		any = true
	}
	if cmd.Flags().Changed("response-fields") {
		v, err := cmd.Flags().GetStringSlice("response-fields")
		if err != nil {
			return nil, err
		}
		opts.ResponseFields = v
		any = true
	}
	if !any {
		return nil, nil
	}
	return opts, nil
}

func printBatchID(w io.Writer, id string) {
	stf := output.StylerFor(w)
	label := stf(lipgloss.NewStyle().Foreground(lipgloss.Color("241"))).Render("Batch ID:")
	fmt.Fprintf(w, "%s %s\n", label, id)
}

// Fast-then-slow polling: short interval for the first fastPollWindow, then back off.
const (
	fastPollInterval = 1 * time.Second
	slowPollInterval = 5 * time.Second
	fastPollWindow   = 10 * time.Second
)

// waitForCompletion polls until completion. Progress goes to stderr so piped stdout stays clean.
func waitForCompletion(ctx context.Context, client *api.Client, id string, jsonMode bool, progressOut io.Writer) (*api.BatchStatus, error) {
	if progressOut == nil {
		progressOut = os.Stderr
	}
	uiEnabled := !jsonMode

	var (
		bar       *ui.Bar
		lastTotal int
	)
	start := time.Now()

	queueSpinner := ui.NewTo(progressOut, "Queued")
	if uiEnabled {
		queueSpinner.Start()
	}

	for {
		// partial=false: stays in "processing" shape until the whole batch finishes,
		// giving reliable counts. partial=true would signal done as soon as any result
		// is ready, catching the batch mid-run.
		s, err := client.Batch(ctx, id, false)
		if err != nil {
			return nil, err
		}

		if uiEnabled && s.Total > 0 {
			if bar == nil || s.Total != lastTotal {
				queueSpinner.Stop()
				bar = ui.NewBar(progressOut, 40)
				bar.SetMessage(fmt.Sprintf("Verifying %d emails", s.Total))
				bar.Start()
				lastTotal = s.Total
			}
			bar.Set(s.Processed, s.Total)
		}

		if s.IsComplete() {
			queueSpinner.Stop()

			// Counts-match completion can race with the API switching to the
			// "completed" payload; retry briefly to get the canonical shape with Emails.
			for i := 0; i < 3 && s.Total > 0 && len(s.Emails) == 0; i++ {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(500 * time.Millisecond):
				}
				next, nerr := client.Batch(ctx, id, false)
				if nerr != nil {
					break
				}
				s = next
			}

			if bar != nil {
				bar.Stop()
			}
			return s, nil
		}

		interval := slowPollInterval
		if time.Since(start) < fastPollWindow {
			interval = fastPollInterval
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
	}
}

func saveToFile(cmd *cobra.Command, cctx *cmdCtx, v any, path string) error {
	n, err := output.WriteResults(v, output.SaveOptions{
		Path:      path,
		ForceJSON: cctx.JSONMode,
	})
	if err != nil {
		return err
	}
	if !cctx.JSONMode {
		h := &output.Human{W: cmd.ErrOrStderr(), Quiet: cctx.Quiet}
		msg := savedMessage(n, path)
		return h.Success(msg)
	}
	return nil
}

func savedMessage(n int, path string) string {
	switch {
	case n <= 0:
		return fmt.Sprintf("Saved to %s", path)
	case n == 1:
		return fmt.Sprintf("Saved 1 result to %s", path)
	default:
		return fmt.Sprintf("Saved %d results to %s", n, path)
	}
}

func renderBatchOutcome(cmd *cobra.Command, cctx *cmdCtx, status *api.BatchStatus, batchID, outPath string, showAll bool) error {
	if outPath != "" {
		return saveToFile(cmd, cctx, status, outPath)
	}
	if cctx.JSONMode {
		return newOutput(cmd.OutOrStdout(), true).Print(status)
	}
	if status.DownloadFile != "" || len(status.Emails) == 0 {
		return newOutput(cmd.OutOrStdout(), false).Print(status)
	}

	h := &output.Human{W: cmd.OutOrStdout(), Quiet: cctx.Quiet}
	if err := h.PrintBatchSummary(status); err != nil {
		return err
	}

	if showAll {
		if _, err := fmt.Fprintln(cmd.OutOrStdout()); err != nil {
			return err
		}
		if err := h.PrintBatchResults(status.Emails); err != nil {
			return err
		}
		if status.IsComplete() {
			return nil
		}
		return h.Hint(fmt.Sprintf("Re-run `emailable batch get %s --partial` for an updated snapshot, or `--wait` to block until complete.", batchID))
	}

	if !status.IsComplete() {
		return h.Hint(fmt.Sprintf("Re-run `emailable batch get %s --partial` for an updated snapshot, `--all` to print rows so far, or `--wait` to block until complete.", batchID))
	}
	return h.Hint(fmt.Sprintf("Run `emailable batch get %s --all` for the full table, or `-o results.csv` to save.", batchID))
}
