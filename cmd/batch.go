package cmd

import (
	"context"
	"encoding/json"
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

// newBatchCmd returns the `emailable batch ...` group with `verify` and
// `get` subcommands. The command surface mirrors the Emailable API:
//
//   - POST /v1/batch → `batch verify EMAIL_OR_FILE [EMAIL_OR_FILE...]`
//   - GET  /v1/batch → `batch get BATCH_ID` (the API uses a single endpoint
//     for both in-progress status and finished results; the CLI does the
//     same instead of splitting them into separate commands)
//
// `batch verify`:
//   - parse positional args via collectEmails (literals, .csv/.json/.txt
//     files; --field selects CSV column / JSON key)
//   - call api.SubmitBatch
//   - default: print "Batch submitted: <id>" + hint (or {"id": "..."} JSON)
//   - --wait: poll status via api.Batch (interval 5s), show progress bar
//     (suppressed in JSON mode); render the final results
//
// `batch get`:
//   - default: api.Batch(id, partial:false) once, render via the formatter.
//     If the batch is still processing → status payload; if complete →
//     per-email results (or download_file hint for >1000 emails).
//   - --partial: api.Batch(id, partial:true) — server-side flag that
//     returns whatever results are ready so far.
//   - --wait: poll until complete with a progress bar; final render uses
//     the same outcome path as the non-wait case.
//   - --all / -o: display preferences on the rendered results.
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
			// Resolve --stream first so its implied --json flows into the
			// effective JSON value we thread through the rest of the call.
			wait, _ := cmd.Flags().GetBool("wait")
			partial, _ := cmd.Flags().GetBool("partial")
			outPath, _ := cmd.Flags().GetString("output")
			showAll, _ := cmd.Flags().GetBool("all")
			stream, _ := cmd.Flags().GetBool("stream")
			wait, jsonEff := applyStreamImplications(stream, wait, jsonOutput)

			cctx, err := newCmdCtxFor(cmd, jsonEff)
			if err != nil {
				return err
			}
			client, err := cctx.requireAuth()
			if err != nil {
				return err
			}

			if wait {
				if partial {
					return fmt.Errorf("--wait and --partial can't be combined: --wait already polls until completion")
				}
				sw := newStreamerIfEnabled(cmd, stream)
				s, err := waitForCompletion(cmd.Context(), client, id, jsonEff, sw)
				if err != nil {
					return err
				}
				if sw != nil {
					return sw.emitComplete(id, s)
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
	get.Flags().Bool("stream", false, "Emit one JSON event per line while polling (implies --wait and --json)")

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
  emailable batch verify alice@example.com bob@example.com

  # Stream NDJSON progress events to stdout
  emailable batch verify emails.csv --stream`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve --stream first so its implied --json flows into the
			// effective JSON value we thread through the rest of the call.
			field, _ := cmd.Flags().GetString("field")
			wait, _ := cmd.Flags().GetBool("wait")
			outPath, _ := cmd.Flags().GetString("output")
			showAll, _ := cmd.Flags().GetBool("all")
			stream, _ := cmd.Flags().GetBool("stream")
			wait, jsonEff := applyStreamImplications(stream, wait, jsonOutput)

			cctx, err := newCmdCtxFor(cmd, jsonEff)
			if err != nil {
				return err
			}
			client, err := cctx.requireAuth()
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

			f := output.New(cmd.OutOrStdout(), jsonEff)

			submit, err := client.SubmitBatch(cmd.Context(), emails, submitOpts)
			if err != nil {
				return err
			}

			if wait {
				// Surface the batch id BEFORE polling so a ctrl-c during
				// the queue/verify phase still leaves the id on screen for
				// a later `batch get <id>`. Suppressed in JSON mode so
				// scripted output stays a single object, and in quiet mode
				// since the id is "chrome" in that context.
				if !jsonEff && !cctx.Quiet {
					printBatchID(cmd.ErrOrStderr(), submit.ID)
				}
				sw := newStreamerIfEnabled(cmd, stream)
				if sw != nil {
					if err := sw.emitSubmitted(submit.ID); err != nil {
						return err
					}
				}
				// waitForCompletion polls partial=false until the batch is
				// done. The final poll IS the canonical completed payload
				// (emails populated).
				final, err := waitForCompletion(cmd.Context(), client, submit.ID, jsonEff || cctx.Quiet, sw)
				if err != nil {
					return err
				}
				if sw != nil {
					return sw.emitComplete(submit.ID, final)
				}
				return renderBatchOutcome(cmd, cctx, final, submit.ID, outPath, showAll)
			}

			if jsonEff {
				return f.Print(map[string]string{"id": submit.ID})
			}
			return f.Print(submit)
		},
	}
	verify.Flags().String("field", "", "CSV column or JSON key `<name>` holding the email (defaults to email)")
	verify.Flags().Bool("wait", false, "Poll until the batch completes")
	verify.Flags().StringP("output", "o", "", "Write results to FILE (.csv or .json; format inferred from extension)")
	verify.Flags().Bool("all", false, "Print the full results table inline instead of a summary")
	verify.Flags().Bool("stream", false, "Emit one JSON event per line while polling (implies --wait and --json)")
	// API parameters for POST /v1/batch. As with the verify command, we only
	// forward flags the user explicitly set so server defaults stay in play.
	verify.Flags().String("url", "", "URL that will receive the batch results via HTTP POST")
	verify.Flags().Bool("retries", true, "Retry verifications when mail servers return certain responses, increasing accuracy")
	verify.Flags().StringSlice("response-fields", nil, "Fields to include in the response (default: all)")

	batch.AddCommand(get, verify)
	return batch
}

// submitBatchOptionsFromFlags assembles api.SubmitBatchOptions from the
// `batch verify` flag set, leaving fields unset for flags the user didn't
// touch. See verifyOptionsFromFlags for the same rationale.
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

// printBatchID writes a one-time `Batch ID: <id>` line — gh/stripe-style
// key/value with the label dimmed and the id rendered plain. Printed by
// callers that have just submitted (or otherwise surfaced) a batch id,
// before the bar/spinner starts. The id stays visible in scrollback
// after animations are torn down, so a ctrl-c mid-wait still leaves the
// user with the id they need to `batch get` later.
func printBatchID(w io.Writer, id string) {
	stf := output.StylerFor(w)
	label := stf(lipgloss.NewStyle().Foreground(lipgloss.Color("241"))).Render("Batch ID:")
	fmt.Fprintf(w, "%s %s\n", label, id)
}

// batchStreamer emits NDJSON events (one JSON object per line) to its
// underlying writer for `batch verify --wait --stream` and
// `batch get --wait --stream`. An agent piping the CLI's stdout into a
// JSON parser sees `submitted`, `progress`, and `complete` events as the
// batch advances, instead of waiting for one giant object at the end.
//
// Each event carries an "event" discriminator plus the batch id; the
// `complete` event embeds the full BatchStatus payload (or a download_file
// URL for >1000-row batches) so consumers don't need a second call.
type batchStreamer struct {
	w io.Writer
}

// newStreamerIfEnabled returns a batchStreamer writing to the command's
// stdout when stream is true, otherwise nil. applyStreamImplications has
// already flipped --json + --wait on when --stream was set, so this is
// purely a constructor at this point.
func newStreamerIfEnabled(cmd *cobra.Command, stream bool) *batchStreamer {
	if !stream {
		return nil
	}
	return &batchStreamer{w: cmd.OutOrStdout()}
}

// emit writes payload as one JSON line followed by a newline. Used by
// emitSubmitted/emitProgress/emitComplete so the encoding lives in one
// place.
func (s *batchStreamer) emit(payload map[string]any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := s.w.Write(b); err != nil {
		return err
	}
	_, err = s.w.Write([]byte("\n"))
	return err
}

// emitSubmitted is the first event of a `batch verify --wait --stream`
// pipeline. `batch get` skips this since the id was already known.
func (s *batchStreamer) emitSubmitted(id string) error {
	return s.emit(map[string]any{"event": "submitted", "id": id})
}

// emitProgress is fired once per poll that observes a known total. The
// final progress tick (processed == total) is followed by `complete`.
func (s *batchStreamer) emitProgress(id string, processed, total int) error {
	return s.emit(map[string]any{
		"event":     "progress",
		"id":        id,
		"processed": processed,
		"total":     total,
	})
}

// emitComplete is the terminal event. The shape mirrors the existing
// `batch get --json` payload (status, reason_counts, emails OR
// download_file), with a leading "event":"complete" + "id" pair so
// streaming consumers can demultiplex against earlier events.
func (s *batchStreamer) emitComplete(id string, status *api.BatchStatus) error {
	payload := map[string]any{
		"event": "complete",
		"id":    id,
	}
	if status.Status != "" {
		payload["status"] = status.Status
	}
	if len(status.Reason) > 0 {
		payload["reason_counts"] = status.Reason
	}
	if len(status.Emails) > 0 {
		payload["emails"] = status.Emails
	}
	if status.DownloadFile != "" {
		payload["download_file"] = status.DownloadFile
	}
	return s.emit(payload)
}

// applyStreamImplications turns on --wait and --json automatically when
// --stream is set. Both are non-negotiable preconditions for streaming
// (nothing to stream without polling; events are JSON so human formatting
// can't render them), and erroring on a missing flag was annoying without
// being useful — the right answer is always to enable them.
//
// Returns the effective (waitOut, jsonOut) pair given the inputs. The
// jsonIn parameter is the user-supplied --json value; jsonOut is what the
// caller should thread through every downstream output-format decision
// (output.New, jsonOutput-style branches). No package-level state is
// touched — the previous version mutated jsonOutput in place, which made
// the data flow hard to follow and forced tests to save/restore the
// global.
func applyStreamImplications(stream, wait, jsonIn bool) (waitOut, jsonOut bool) {
	if !stream {
		return wait, jsonIn
	}
	return true, true
}

// Polling schedule for waitForCompletion. The first fastPollWindow of the
// wait uses fastPollInterval so small batches (which often finish in a few
// seconds) return promptly; after that we back off to slowPollInterval to
// avoid hammering the API on long-running batches.
const (
	fastPollInterval = 1 * time.Second
	slowPollInterval = 5 * time.Second
	fastPollWindow   = 10 * time.Second
)

// waitForCompletion polls the batch status until processing is complete,
// rendering a progress bar in non-JSON mode. Returns the final status.
//
// Polling cadence: 1s for the first 10 seconds, then 5s thereafter. See the
// constants above.
//
// The progress bar and any status lines are written to stderr so that piping
// stdout (e.g. `verify --wait > results.json`) doesn't mix the bar into the
// result payload.
//
// When sw is non-nil (i.e. --stream is set), one `progress` NDJSON event is
// emitted per poll that observes a known total. The progress bar is
// suppressed in that mode since the caller is asking for machine output.
func waitForCompletion(ctx context.Context, client *api.Client, id string, jsonMode bool, sw *batchStreamer) (*api.BatchStatus, error) {
	progressOut := io.Writer(os.Stderr)
	// In stream mode we hand visual progress to the agent's NDJSON parser,
	// so suppress the bar/spinner entirely.
	uiEnabled := !jsonMode && sw == nil

	var (
		bar       *ui.Bar
		lastTotal int
	)
	start := time.Now()

	// Queued-phase spinner: animates while we're polling with total=0 (server
	// hasn't started processing yet). Stops as soon as a bar exists.
	queueSpinner := ui.NewTo(progressOut, "Queued")
	if uiEnabled {
		queueSpinner.Start()
	}

	for {
		// We poll with partial=false intentionally. The Emailable API returns
		// two different shapes for partial=true vs partial=false:
		//   - partial=true returns the "completed" payload (total=0,
		//     emails populated) the moment ANY results are ready, even while
		//     the rest of the batch is still in flight. Treating that as done
		//     causes the final re-fetch to catch the batch mid-processing.
		//   - partial=false stays in the "processing" payload (total=N,
		//     processed<N, no emails) until the whole batch is actually
		//     finished, then switches to the completed payload.
		// partial=false also gives us reliable processed/total counts during
		// processing, which is exactly what the progress bar needs.
		s, err := client.Batch(ctx, id, false)
		if err != nil {
			return nil, err
		}

		if uiEnabled && s.Total > 0 {
			if bar == nil || s.Total != lastTotal {
				// First time we have a known total — kill the queued spinner
				// before drawing the bar so the two don't fight for the line.
				queueSpinner.Stop()

				// Fixed 40-cell bar, pip/cargo-style. Full-terminal-width
				// bars feel like a "wall" for short jobs; a constrained
				// bar reads as a progress indicator rather than the
				// dominant UI element. Auto-caps to terminal width on
				// narrow shells.
				bar = ui.NewBar(progressOut, 40)
				bar.SetMessage(fmt.Sprintf("Verifying %d emails", s.Total))
				bar.Start()
				lastTotal = s.Total
			}
			bar.Set(s.Processed, s.Total)
		}

		// NDJSON progress event: emitted on every poll that has a known total
		// so an agent sees movement even before the bar would normally pick
		// up the count. Errors writing to the stream are non-fatal; we keep
		// polling.
		if sw != nil && s.Total > 0 {
			_ = sw.emitProgress(id, s.Processed, s.Total)
		}

		if s.IsComplete() {
			// Stop animations BEFORE returning so the final 100% frame
			// is rendered and a newline written.
			queueSpinner.Stop()

			// Counts-match completion (total>0 && processed>=total) can race
			// with the API switching to the "completed" payload that carries
			// the Emails slice. Retry briefly so callers always get the
			// canonical completed shape instead of an empty Emails snapshot.
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
				// The bar self-erases on Stop and the caller (the
				// summary line via PrintBatchSummary) is the canonical
				// "done" signal — no need to retitle the bar before
				// tearing it down.
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

// saveToFile dispatches to output.WriteResults and prints a success line to
// stderr in non-JSON mode. Used by the batch subcommands' --output flag.
//
// cctx carries the effective JSON/Quiet decisions for this invocation. The
// success line is suppressed in JSON mode (scripted output) and in quiet
// mode (chrome-suppression policy).
func saveToFile(cmd *cobra.Command, cctx *cmdCtx, v any, path string) error {
	n, err := output.WriteResults(v, output.SaveOptions{
		Path:      path,
		ForceJSON: cctx.JSONMode,
	})
	if err != nil {
		return err
	}
	if !cctx.JSONMode {
		// Success goes to stderr so stdout (the actual results file path
		// or JSON payload) stays scriptable.
		h := &output.Human{W: cmd.ErrOrStderr(), Quiet: cctx.Quiet}
		msg := savedMessage(n, path)
		return h.Success(msg)
	}
	return nil
}

// savedMessage returns the human "Saved …" line for a successful file
// write. Singular/plural for known row counts; falls back to a count-free
// "Saved to <file>" when the count is unknown (e.g. account JSON dumps).
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

// renderBatchOutcome handles the final stdout/file rendering for a batch
// retrieval (batch get, batch get --wait, batch verify --wait).
//
// Decision tree:
//
//   - --output FILE        → write to file via saveToFile.
//   - --json               → dump full BatchStatus to stdout as JSON.
//   - DownloadFile is set  → human formatter renders the download URL hint.
//   - No Emails yet        → human formatter renders the in-progress card.
//
// When Emails are present the output always leads with the one-line summary
// (✓ Verified N emails or ⋯ Partial results …). Below that:
//
//   - --all  → the full per-email results table follows the summary.
//   - else   → just the summary (a tip points at --all for the full table,
//     or, when partial, at re-running for an updated snapshot / --wait).
//
// Partial-results note: a `batch get --partial` response carries Emails plus
// progress under TotalCounts, so it lands on the summary path the same as a
// completed batch — PrintBatchSummary distinguishes via BatchStatus.IsComplete.
// The table is opt-in via --all regardless of batch size — past UX feedback
// was that the table is rarely useful at a glance, and the summary reads
// better for both small and large batches.
func renderBatchOutcome(cmd *cobra.Command, cctx *cmdCtx, status *api.BatchStatus, batchID, outPath string, showAll bool) error {
	if outPath != "" {
		return saveToFile(cmd, cctx, status, outPath)
	}
	if cctx.JSONMode {
		return output.New(cmd.OutOrStdout(), true).Print(status)
	}
	if status.DownloadFile != "" || len(status.Emails) == 0 {
		// Either the >1000-emails download case, or a status without
		// per-email results (still-processing batch). Defer to the
		// formatter's dispatch.
		return output.New(cmd.OutOrStdout(), false).Print(status)
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
	// Default: summary only. The tip surfaces --all so users discover the
	// full-table option without reading --help.
	return h.Hint(fmt.Sprintf("Run `emailable batch get %s --all` for the full table, or `-o results.csv` to save.", batchID))
}
