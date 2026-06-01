package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/emailable/emailable-cli/internal/env"
	"github.com/emailable/emailable-cli/internal/output"
	"github.com/emailable/emailable-cli/internal/ui"
	"github.com/emailable/emailable-cli/internal/updater"
	"github.com/spf13/cobra"
)

// version, commit, and buildDate are injected via -ldflags -X (GoReleaser sets
// all three); they fall back to runtime/debug VCS info for local `make build`.
// An injected commit is trusted clean — GoReleaser's before-hooks dirty the
// worktree, which would otherwise stamp releases "-dirty".
var (
	version   = "dev"
	commit    = ""
	buildDate = ""
)

// releaseURLPrefix is the GitHub releases URL we link to from --version output.
const releaseURLPrefix = "https://github.com/emailable/emailable-cli/releases/tag/"

// jsonOutput is the value of the persistent --json flag. Commands read this
// (rather than re-querying the cobra flag set) to pick an output formatter.
var jsonOutput bool

// jqExpr backs the --jq flag; jqQuery is its compiled form. --jq implies --json.
var (
	jqExpr  string
	jqQuery *output.Query
)

func newOutput(w io.Writer, jsonMode bool) output.Formatter {
	if jqQuery != nil {
		return &output.JSON{W: w, Query: jqQuery}
	}
	return output.New(w, jsonMode)
}

func newJSON(w io.Writer) *output.JSON {
	return &output.JSON{W: w, Query: jqQuery}
}

// apiKey is the value of the `login --api-key` local flag. It is deliberately
// NOT a persistent root flag: credentials on argv would leak into shell history
// and `ps` output, so a key only comes via EMAILABLE_API_KEY, stored config, or
// `login` (flag or stdin pipe).
var apiKey string

// debugMode is the value of the persistent --debug flag. When true (or when
// EMAILABLE_DEBUG is set non-empty) the API client dumps each request and
// response to stderr with the Authorization header redacted.
var debugMode bool

// quietMode is the value of the persistent --quiet / -q flag. When true the
// human-mode "chrome" lines (Success / Hint / Notice) are suppressed; errors
// still print and --json output is unaffected (quiet is a human-mode-only
// modifier). Mirrors the convention in curl, docker, gh.
var quietMode bool

// Command group IDs. Used both by cobra's command grouping and by the custom
// help renderer to emit gh-style section headers in a stable order.
const (
	groupCore    = "core"
	groupAuth    = "auth"
	groupExtras  = "extras"
	groupHelpers = "helpers" // built-in cobra commands (help, completion)
)

// versionDisplay returns the multi-line version blurb used by both `--version`
// and the `version` subcommand.
func versionDisplay() string {
	// Use the resolved version (collectVersionInfo falls back to the Go
	// toolchain's module version for `go install` builds where ldflags
	// weren't injected).
	v := collectVersionInfo().Version

	var b strings.Builder
	b.WriteString("emailable version ")
	b.WriteString(v)

	if extras := versionExtras(); extras != "" {
		b.WriteString(" (")
		b.WriteString(extras)
		b.WriteString(")")
	}

	if e, err := env.Current(); err == nil && e.Name != "default" {
		b.WriteString(" [")
		b.WriteString(e.Name)
		b.WriteString("]")
	}

	if v != "" && v != "dev" {
		tag := v
		if !strings.HasPrefix(tag, "v") {
			tag = "v" + tag
		}
		b.WriteString("\n")
		b.WriteString(releaseURLPrefix)
		b.WriteString(tag)
	}

	return b.String()
}

// versionInfo holds the structured pieces that make up the version blurb.
// Used by versionExtras (for the human string) and by `version --json` to
// emit a machine-readable representation. Fields are zero-valued when the
// corresponding data isn't available (e.g. no VCS info in a stripped build).
type versionInfo struct {
	Version   string
	BuildDate string // either ldflags-injected buildDate or vcs.time (YYYY-MM-DD)
	Commit    string // short (7-char) VCS revision, when available
	Dirty     bool   // vcs.modified — only meaningful when Commit is set
}

// collectVersionInfo gathers version metadata from ldflags and VCS build info.
func collectVersionInfo() versionInfo {
	vi := versionInfo{Version: version, BuildDate: buildDate, Commit: commit}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return vi
	}
	fromVCS := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			fromVCS = true
			if vi.Commit == "" {
				if len(s.Value) > 7 {
					vi.Commit = s.Value[:7]
				} else {
					vi.Commit = s.Value
				}
			}
		case "vcs.time":
			if vi.BuildDate == "" {
				if t, err := time.Parse(time.RFC3339, s.Value); err == nil {
					vi.BuildDate = t.Format("2006-01-02")
				}
			}
		case "vcs.modified":
			// Only a VCS-derived commit can be dirty; an injected one is clean.
			if commit == "" {
				vi.Dirty = s.Value == "true"
			}
		}
	}
	// `go install module@vX.Y.Z` injects no ldflags and builds from the module
	// cache (no vcs.* settings), so the package-level `version` is still "dev".
	// Fall back to the toolchain-recorded module version there. A local checkout
	// always carries VCS info — its Main.Version is an untagged pseudo-version,
	// not a real release, so leave it as "dev" rather than print a 404 tag URL.
	if !fromVCS && (vi.Version == "" || vi.Version == "dev") &&
		info.Main.Version != "" && info.Main.Version != "(devel)" {
		vi.Version = strings.TrimPrefix(info.Main.Version, "v")
	}
	return vi
}

// versionExtras returns the date / commit info shown in parens after the
// version number. Prefers ldflags-injected values (GoReleaser); falls back to
// runtime/debug.ReadBuildInfo VCS data for local checkouts.
func versionExtras() string {
	vi := collectVersionInfo()
	var parts []string
	if vi.Commit != "" {
		label := "commit " + vi.Commit
		if vi.Dirty {
			label += "-dirty"
		}
		parts = append(parts, label)
	}
	if vi.BuildDate != "" {
		parts = append(parts, vi.BuildDate)
	}
	return strings.Join(parts, ", ")
}

// longDescription is the root help blurb. Intentionally short and feature-
// agnostic so it doesn't go stale as the product grows.
const longDescription = "Command-line interface for Emailable's email verification API."

// newRootCmd returns a fresh root cobra.Command. Used by Execute and by tests.
// The v argument lets tests inject a deterministic version string; production
// callers pass the package-level `version` (set via ldflags).
func newRootCmd(v string) *cobra.Command {
	resetRootFlagState()

	// Swap the package-level version so versionDisplay and the version
	// subcommand's lazily-read RunE observe v. Intentionally not restored:
	// tests rely on the swap persisting across Execute and don't run
	// newRootCmd in parallel, so leaving it set is safe.
	version = v

	root := &cobra.Command{
		Use:           "emailable",
		Short:         "Command-line interface for Emailable",
		Long:          longDescription,
		Version:       versionDisplay(),
		SilenceUsage:  true,
		SilenceErrors: true,
		// A bare `emailable` either onboards a logged-out user on a TTY or
		// prints help; see runRootDefault.
		RunE: runRootDefault,
		// Resolve the default output format before any RunE runs. Precedence:
		// --json flag > EMAILABLE_OUTPUT > config `output` > "human".
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("json") {
				merged, err := env.MergedConfig()
				if err != nil {
					return err
				}
				if strings.EqualFold(merged.Output, "json") {
					jsonOutput = true
				}
			}
			// Clear first so a query from an earlier in-process run can't leak
			// into a command invoked without --jq.
			jqQuery = nil
			if jqExpr != "" {
				// Set JSON mode before compiling so a bad-expression error
				// still renders as JSON, honoring --jq's implied --json.
				jsonOutput = true
				q, err := output.CompileQuery(jqExpr)
				if err != nil {
					return NewInvalidInputf("invalid --jq expression: %v", err)
				}
				jqQuery = q
			}
			return nil
		},
	}
	// Print just the blurb; versionDisplay already includes "emailable version ".
	root.SetVersionTemplate("{{ .Version }}\n")

	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Return JSON response")
	root.PersistentFlags().StringVar(&jqExpr, "jq", "", "Filter JSON output with a jq `expression` (implies --json)")
	root.PersistentFlags().BoolVar(&debugMode, "debug", false, "Dump HTTP requests/responses to stderr (also EMAILABLE_DEBUG)")
	root.PersistentFlags().BoolVarP(&quietMode, "quiet", "q", false, "Suppress non-error human output (success lines, hints, progress)")

	// Register groups so cobra knows about them; the custom usage func is
	// what actually renders them under gh-style headings.
	root.AddGroup(
		&cobra.Group{ID: groupCore, Title: "CORE COMMANDS"},
		&cobra.Group{ID: groupAuth, Title: "AUTHENTICATION COMMANDS"},
		&cobra.Group{ID: groupExtras, Title: "ADDITIONAL COMMANDS"},
	)

	verify := newVerifyCmd()
	verify.GroupID = groupCore
	batch := newBatchCmd()
	batch.GroupID = groupCore
	account := newAccountCmd()
	account.GroupID = groupCore

	login := newLoginCmd()
	login.GroupID = groupAuth
	logout := newLogoutCmd()
	logout.GroupID = groupAuth
	status := newStatusCmd()
	status.GroupID = groupAuth

	versionSub := newVersionCmd()
	versionSub.GroupID = groupExtras

	skillSub := newSkillCmd()
	skillSub.GroupID = groupExtras

	root.AddCommand(verify, batch, account, login, logout, status, versionSub, skillSub, newManCmd())

	// Hide cobra's auto-generated `completion` command from --help. It's
	// still callable, just doesn't clutter the curated command list.
	root.CompletionOptions.HiddenDefaultCmd = true

	// Route both usage errors and --help/`help` through the same renderer so
	// they produce identical output.
	root.SetUsageFunc(func(c *cobra.Command) error {
		return renderHelp(c, c.OutOrStderr())
	})
	root.SetHelpFunc(func(c *cobra.Command, _ []string) {
		_ = renderHelp(c, c.OutOrStdout())
	})

	return root
}

func resetRootFlagState() {
	jsonOutput = false
	jqExpr = ""
	jqQuery = nil
	apiKey = ""
	debugMode = false
	quietMode = false
}

// Execute runs the root command.
//
// Cobra's default error rendering is silenced so every RunE error is routed
// through renderError (stderr only). Exit code is non-zero on any error.
func Execute() {
	root := newRootCmd(version)

	// Skip the network call entirely for opt-outs knowable before flags parse.
	// Uses IsTerminal, not IsTTY, so NO_COLOR (a styling pref) still gets checks.
	// JSONMode/Quiet are flag-derived and gate only the notice, below.
	preSkip := updater.ShouldSkip(updater.Conditions{
		CurrentVersion: version,
		StderrTTY:      ui.IsTerminal(root.ErrOrStderr()),
		OptOut:         env.UpdateNotifierOptOut(),
	})

	// Kick off the update check in parallel with command execution; resultCh
	// carries the outcome. The post-command grace block tolerates a hung check.
	updCtx, updCancel := context.WithCancel(context.Background())
	defer updCancel()
	resultCh := make(chan updater.Result, 1)
	if preSkip == updater.SkipNone {
		// Run even when we may end up skipping the notice (e.g. JSON mode) so
		// the cache still refreshes; the gate below decides on printing.
		go func() {
			resultCh <- updater.Check(updCtx, version, updater.CacheDir())
		}()
	}

	runErr := root.Execute()

	// Re-check with the now-parsed flags to gate the notice. If the pre-flight
	// already opted out, no goroutine ran, so keep that reason.
	skip := preSkip
	if skip == updater.SkipNone {
		skip = updater.ShouldSkip(updater.Conditions{
			CurrentVersion: version,
			JSONMode:       jsonOutput,
			Quiet:          quietMode,
			StderrTTY:      ui.IsTerminal(root.ErrOrStderr()),
			OptOut:         env.UpdateNotifierOptOut(),
		})
	}

	if runErr != nil {
		renderError(root.ErrOrStderr(), runErr, jsonOutput)
		// Best-effort notice on error path too, but only if the gate
		// allows. Print BEFORE exiting; cap the wait at 1s.
		if skip == updater.SkipNone {
			waitAndNotify(root.ErrOrStderr(), resultCh, updCancel, updateNoticeWait)
		}
		os.Exit(exitCode(runErr))
	}

	if skip != updater.SkipNone {
		// Nothing to print; the background goroutine may still be running
		// (e.g. mid-fetch). It'll be torn down by updCancel via the
		// deferred cancel above, so we exit cleanly.
		return
	}
	waitAndNotify(root.ErrOrStderr(), resultCh, updCancel, updateNoticeWait)
}

// updateNoticeWait is the hard ceiling on how long Execute will block at
// shutdown waiting for the update goroutine to deliver a result. 1s matches
// the spec ("up to 1 second extra"). If the check is still running when
// this elapses, we abandon and exit — never delaying the user.
const updateNoticeWait = 1 * time.Second

// waitAndNotify blocks for at most wait, then prints the update notice (if the
// check finished and produced one) or returns silently. updCancel lets a
// still-running fetch shut itself down promptly.
func waitAndNotify(w io.Writer, resultCh <-chan updater.Result, updCancel context.CancelFunc, wait time.Duration) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case r := <-resultCh:
		_ = updater.MaybeNotify(w, r, ui.IsTTY(w))
	case <-timer.C:
		// Still pending; abandon. Cancel the in-flight HTTP so the
		// goroutine doesn't leak past process exit.
		updCancel()
	}
}

// renderHelp writes a gh-style help screen for c to w. TTY detection is
// performed once against w so ANSI escape codes are suppressed when output
// is piped, redirected, or captured by tests via a bytes.Buffer.
func renderHelp(c *cobra.Command, w io.Writer) error {
	tty := ui.IsTTY(w)
	var b strings.Builder

	long := c.Long
	if long == "" {
		long = c.Short
	}
	if long != "" {
		b.WriteString(long)
		b.WriteString("\n\n")
	}

	// USAGE
	b.WriteString(ui.Heading("USAGE", tty))
	b.WriteByte('\n')
	b.WriteString("  ")
	b.WriteString(c.UseLine())
	if c.HasAvailableSubCommands() {
		b.WriteString(" <command> [flags]")
	}
	b.WriteString("\n\n")

	// Command groups (only at levels with subcommands).
	if c.HasAvailableSubCommands() {
		writeGroupedCommands(&b, c, tty)
	}

	// Local flags only by default; persistent flags from root would repeat on
	// every subcommand. On root, LocalFlags already includes the persistent ones.
	flags := c.LocalFlags()
	if c.HasAvailableInheritedFlags() {
		// Merge inherited persistent flags so subcommand help still shows them.
		flags.AddFlagSet(c.InheritedFlags())
	}
	if flags.HasAvailableFlags() {
		b.WriteString(ui.Heading("FLAGS", tty))
		b.WriteByte('\n')
		b.WriteString(indent(flags.FlagUsages(), "  "))
		b.WriteByte('\n')
	}

	// EXAMPLES + LEARN MORE only on the root command — gh follows the same
	// pattern. Subcommand-specific examples live in each command's Example
	// field if/when they're added.
	if !c.HasParent() {
		b.WriteString(ui.Heading("EXAMPLES", tty))
		b.WriteByte('\n')
		for _, line := range []string{
			"$ emailable login",
			"$ emailable verify hello@example.com",
			"$ emailable batch verify emails.csv --wait",
			"$ emailable account status --json",
		} {
			b.WriteString("  ")
			b.WriteString(ui.Dim(line, tty))
			b.WriteByte('\n')
		}
		b.WriteByte('\n')

		b.WriteString(ui.Heading("LEARN MORE", tty))
		b.WriteByte('\n')
		b.WriteString("  Read the docs at ")
		b.WriteString(ui.Cyan("https://emailable.com/docs/api", tty))
		b.WriteByte('\n')
	} else if c.Example != "" {
		b.WriteString(ui.Heading("EXAMPLES", tty))
		b.WriteByte('\n')
		b.WriteString(indent(c.Example, "  "))
		b.WriteByte('\n')
	}

	_, err := fmt.Fprint(w, b.String())
	return err
}

// writeGroupedCommands renders the subcommand listing, grouped by GroupID and
// in the same order groups were registered on the root. Commands without a
// group (or whose group isn't registered on the parent) fall into a generic
// "COMMANDS" section so nothing is dropped.
func writeGroupedCommands(b *strings.Builder, c *cobra.Command, tty bool) {
	type bucket struct {
		title string
		cmds  []*cobra.Command
	}

	// Preserve registered group order. For non-root commands (e.g. `batch`)
	// there are no groups; everything falls through to the default bucket.
	order := make([]string, 0, len(c.Groups()))
	buckets := make(map[string]*bucket, len(c.Groups()))
	for _, g := range c.Groups() {
		order = append(order, g.ID)
		buckets[g.ID] = &bucket{title: g.Title}
	}
	def := &bucket{title: "COMMANDS"}

	for _, sub := range c.Commands() {
		if !sub.IsAvailableCommand() || sub.Name() == "help" {
			continue
		}
		if bkt, ok := buckets[sub.GroupID]; ok {
			bkt.cmds = append(bkt.cmds, sub)
		} else {
			def.cmds = append(def.cmds, sub)
		}
	}

	// Find the widest name across all buckets for nice alignment.
	pad := 0
	for _, id := range order {
		for _, sub := range buckets[id].cmds {
			if n := len(sub.Name()); n > pad {
				pad = n
			}
		}
	}
	for _, sub := range def.cmds {
		if n := len(sub.Name()); n > pad {
			pad = n
		}
	}
	if pad < 8 {
		pad = 8
	}

	writeBucket := func(bkt *bucket) {
		if len(bkt.cmds) == 0 {
			return
		}
		b.WriteString(ui.Heading(bkt.title, tty))
		b.WriteByte('\n')
		for _, sub := range bkt.cmds {
			name := sub.Name()
			b.WriteString("  ")
			b.WriteString(ui.Cyan(name, tty))
			b.WriteString(strings.Repeat(" ", pad-len(name)+2))
			b.WriteString(sub.Short)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	for _, id := range order {
		writeBucket(buckets[id])
	}
	writeBucket(def)
}

// indent prefixes every line of s with prefix. Trailing newline is preserved.
func indent(s, prefix string) string {
	if s == "" {
		return s
	}
	trimmed := strings.TrimRight(s, "\n")
	lines := strings.Split(trimmed, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n") + "\n"
}
