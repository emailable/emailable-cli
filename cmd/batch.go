package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
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
		Args:         unknownSubcommand,
		RunE:         showHelp,
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
		Args:         cobra.ExactArgs(1),
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
			if wait && partial {
				return NewInvalidInput("--wait and --partial can't be combined: --wait already polls until completion")
			}

			cctx, err := newCmdCtxFor(cmd, jsonOutput)
			if err != nil {
				return err
			}
			client, err := cctx.requireAuth(cmd.Context())
			if err != nil {
				return err
			}

			if wait {
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
		Args:         cobra.MinimumNArgs(1),
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
					// Credits are already spent; keep the id attached so the
					// caller can still fetch results with `batch get`.
					return &batchWaitError{ID: submit.ID, Err: err}
				}
				return renderBatchOutcome(cmd, cctx, final, submit.ID, outPath, showAll)
			}

			return f.Print(submit)
		},
	}
	verify.Flags().String("field", "", "CSV column or dotted JSON path `<name>` holding the email, e.g. contacts.email (defaults to email)")
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

// A long --wait shouldn't die on one network blip. Temporary poll failures back
// off from pollFailureBackoff, doubling up to pollFailureMaxBackoff, and we give
// up after maxPollFailures in a row. The outer delays alone are 2+4+8+16+30+30+30s,
// about two minutes, and each failed poll also spends the API client's own
// per-request retries, so the real bound is longer.
const (
	maxPollFailures       = 8
	pollFailureBackoff    = 2 * time.Second
	pollFailureMaxBackoff = 30 * time.Second
)

// batchWaitError carries the id of a submitted batch whose --wait failed, so
// the error output still tells the caller which batch to fetch.
type batchWaitError struct {
	ID  string
	Err error
}

func (e *batchWaitError) Error() string { return e.Err.Error() }

func (e *batchWaitError) Unwrap() error { return e.Err }

// isTemporaryPollError reports whether a failed poll is worth repeating:
// network errors, 5xx, 429, and 249. Auth, not-found, and other 4xx are final.
func isTemporaryPollError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		s := apiErr.StatusCode
		return s == 249 || s == http.StatusTooManyRequests || s >= 500
	}
	return isNetworkError(err)
}

func pollFailureDelay(failures int) time.Duration {
	d := pollFailureBackoff << (failures - 1)
	if d <= 0 || d > pollFailureMaxBackoff {
		d = pollFailureMaxBackoff
	}
	return d
}

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
	failures := 0

	queueSpinner := ui.NewTo(progressOut, "Queued")
	if uiEnabled {
		queueSpinner.Start()
	}
	stopUI := func() {
		queueSpinner.Stop()
		if bar != nil {
			bar.Stop()
		}
	}

poll:
	for {
		// partial=false: stays in "processing" shape until the whole batch finishes,
		// giving reliable counts. partial=true would signal done as soon as any result
		// is ready, catching the batch mid-run.
		s, err := client.Batch(ctx, id, false)
		if err != nil {
			failures++
			if !isTemporaryPollError(err) || failures >= maxPollFailures {
				stopUI()
				return nil, err
			}
			if serr := retrySleep(ctx, pollFailureDelay(failures)); serr != nil {
				stopUI()
				return nil, serr
			}
			continue
		}
		failures = 0

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
				if err := retrySleep(ctx, 500*time.Millisecond); err != nil {
					stopUI()
					return nil, err
				}
				next, nerr := client.Batch(ctx, id, false)
				if nerr != nil {
					// Same failure budget as the main poll: a temporary error
					// goes back around rather than returning the count-only
					// shape as if it were the final result.
					failures++
					if !isTemporaryPollError(nerr) || failures >= maxPollFailures {
						stopUI()
						return nil, nerr
					}
					if serr := retrySleep(ctx, pollFailureDelay(failures)); serr != nil {
						stopUI()
						return nil, serr
					}
					continue poll
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
		if err := retrySleep(ctx, interval); err != nil {
			stopUI()
			return nil, err
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
	return reportSaved(cmd, cctx, n, path)
}

func reportSaved(cmd *cobra.Command, cctx *cmdCtx, n int, path string) error {
	if !cctx.JSONMode {
		h := &output.Human{W: cmd.ErrOrStderr(), Quiet: cctx.Quiet}
		msg := savedMessage(n, path)
		return h.Success(msg)
	}
	return nil
}

// saveBatchToFile refuses to write a header-only file for a batch whose rows
// aren't available, and fetches the results file for large batches.
func saveBatchToFile(cmd *cobra.Command, cctx *cmdCtx, status *api.BatchStatus, batchID, path string) error {
	// Only `batch get` has --partial; the lookup fails (false) elsewhere.
	partial, _ := cmd.Flags().GetBool("partial")
	if !status.IsComplete() && !partial {
		if processed, total, ok := status.Progress(); ok {
			return NewInvalidInputf("batch %s is still verifying (%d/%d); use --wait or --partial", batchID, processed, total)
		}
		return NewInvalidInputf("batch %s is still verifying; use --wait or --partial", batchID)
	}
	if status.DownloadFile != "" {
		return saveDownloadToFile(cmd, cctx, status, path)
	}
	if _, total, ok := status.Progress(); ok && total > 0 && status.IsComplete() && len(status.Emails) == 0 {
		return fmt.Errorf("batch %s returned no per-email results to save", batchID)
	}
	return saveToFile(cmd, cctx, status, path)
}

func saveDownloadToFile(cmd *cobra.Command, cctx *cmdCtx, status *api.BatchStatus, path string) error {
	sp := ui.NewTo(cmd.ErrOrStderr(), "Downloading results")
	if !cctx.JSONMode && !cctx.Quiet {
		sp.Start()
	}
	d, err := api.FetchDownload(cmd.Context(), status.DownloadFile, nil)
	sp.Stop()
	if err != nil {
		return err
	}
	n, err := output.WriteDownload(d, status, output.SaveOptions{
		Path:      path,
		ForceJSON: cctx.JSONMode,
	})
	if err != nil {
		return err
	}
	return reportSaved(cmd, cctx, n, path)
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
		return saveBatchToFile(cmd, cctx, status, batchID, outPath)
	}
	if cctx.JSONMode {
		return newOutput(cmd.OutOrStdout(), true).Print(status)
	}
	if status.DownloadFile != "" {
		if err := newOutput(cmd.OutOrStdout(), false).Print(status); err != nil {
			return err
		}
		h := &output.Human{W: cmd.OutOrStdout(), Quiet: cctx.Quiet}
		return h.Hint(fmt.Sprintf("Run `emailable batch get %s -o results.csv` to save it.", batchID))
	}
	if len(status.Emails) == 0 {
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
