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
	"github.com/emailable/emailable-cli/internal/ui"
	"github.com/emailable/emailable-cli/internal/updater"
	"github.com/spf13/cobra"
)

// version and buildDate are injected at link time via -ldflags -X. version
// defaults to "dev"; buildDate is empty for local builds and falls back to VCS
// info via runtime/debug.ReadBuildInfo when present.
var (
	version   = "dev"
	buildDate = ""
)

// releaseURLPrefix is the GitHub releases URL we link to from --version output.
const releaseURLPrefix = "https://github.com/emailable/emailable-cli/releases/tag/"

// jsonOutput is the value of the persistent --json flag. Commands read this
// (rather than re-querying the cobra flag set) to pick an output formatter.
var jsonOutput bool

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
	var b strings.Builder
	b.WriteString("emailable version ")
	b.WriteString(version)

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

	if version != "" && version != "dev" {
		tag := version
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
	vi := versionInfo{Version: version, BuildDate: buildDate}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return vi
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) > 7 {
				vi.Commit = s.Value[:7]
			} else {
				vi.Commit = s.Value
			}
		case "vcs.time":
			if vi.BuildDate == "" {
				if t, err := time.Parse(time.RFC3339, s.Value); err == nil {
					vi.BuildDate = t.Format("2006-01-02")
				}
			}
		case "vcs.modified":
			vi.Dirty = s.Value == "true"
		}
	}
	return vi
}

// versionExtras returns the date / commit info shown in parens after the
// version number. Prefers a release-time ldflags-injected buildDate; falls
// back to runtime/debug.ReadBuildInfo VCS data for local checkouts.
func versionExtras() string {
	// A release-injected buildDate trumps VCS info (and suppresses the commit prefix).
	if buildDate != "" {
		return buildDate
	}
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
			return nil
		},
	}
	// Print just the blurb; versionDisplay already includes "emailable version ".
	root.SetVersionTemplate("{{ .Version }}\n")

	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Return JSON response")
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

	root.AddCommand(verify, batch, account, login, logout, status, versionSub, newManCmd())

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

// Execute runs the root command.
//
// Cobra's default error rendering is silenced so every RunE error is routed
// through renderError (stderr only). Exit code is non-zero on any error.
func Execute() {
	root := newRootCmd(version)

	// Kick off the update check in parallel with command execution; resultCh
	// carries the outcome. The post-command grace block tolerates a hung check.
	updCtx, updCancel := context.WithCancel(context.Background())
	defer updCancel()
	resultCh := make(chan updater.Result, 1)
	go func() {
		// Conservatively run the network call even when we might end up
		// skipping the *notice* (e.g. JSON mode), so the cache refresh
		// still happens. The notice gate below makes the final call.
		resultCh <- updater.Check(updCtx, version, updater.CacheDir())
	}()

	runErr := root.Execute()

	// Decide whether the *notice* should be considered for printing. This
	// is the gate the user perceives — silent skips don't even touch the
	// channel below, so a hung check doesn't delay exit in those cases.
	skip := updater.ShouldSkip(updater.Conditions{
		CurrentVersion: version,
		JSONMode:       jsonOutput,
		Quiet:          quietMode,
		StderrTTY:      ui.IsTTY(root.ErrOrStderr()),
		OptOut:         env.UpdateNotifierOptOut(),
	})

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
