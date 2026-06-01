package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
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

const releaseURLPrefix = "https://github.com/emailable/emailable-cli/releases/tag/"

// Only a clean semver release has a GitHub tag to link to; snapshot and
// pseudo-version builds would 404.
var releaseVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

var jsonOutput bool

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

func versionDisplay() string {
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

	if releaseVersion.MatchString(v) {
		b.WriteString("\n")
		b.WriteString(releaseURLPrefix)
		b.WriteString("v" + v)
	}

	return b.String()
}

type versionInfo struct {
	Version   string
	BuildDate string // either ldflags-injected buildDate or vcs.time (YYYY-MM-DD)
	Commit    string // short (7-char) VCS revision, when available
	Dirty     bool   // vcs.modified — only meaningful when Commit is set
}

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

const longDescription = "Command-line interface for Emailable's email verification API."

// newRootCmd returns a fresh root cobra.Command. The v argument lets tests
// inject a deterministic version string; production callers pass `version`.
func newRootCmd(v string) *cobra.Command {
	resetRootFlagState()

	// Intentionally not restored: tests rely on the swap persisting across
	// Execute and don't run newRootCmd in parallel, so leaving it set is safe.
	version = v

	root := &cobra.Command{
		Use:           "emailable",
		Short:         "Command-line interface for Emailable",
		Long:          longDescription,
		Version:       versionDisplay(),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          runRootDefault,
		// Precedence: --json flag > EMAILABLE_OUTPUT > config `output` > "human".
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
			// Clear first so a query from an earlier in-process run can't leak.
			jqQuery = nil
			if jqExpr != "" {
				// Set JSON mode before compiling so a bad-expression error renders as JSON.
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
	root.SetVersionTemplate("{{ .Version }}\n")

	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Return JSON response")
	root.PersistentFlags().StringVar(&jqExpr, "jq", "", "Filter JSON output with a jq `expression` (implies --json)")
	root.PersistentFlags().BoolVar(&debugMode, "debug", false, "Dump HTTP requests/responses to stderr (also EMAILABLE_DEBUG)")
	root.PersistentFlags().BoolVarP(&quietMode, "quiet", "q", false, "Suppress non-error human output (success lines, hints, progress)")

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

	root.CompletionOptions.HiddenDefaultCmd = true

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
func Execute() {
	root := newRootCmd(version)

	// Uses IsTerminal (not IsTTY) so NO_COLOR doesn't suppress update checks.
	preSkip := updater.ShouldSkip(updater.Conditions{
		CurrentVersion: version,
		StderrTTY:      ui.IsTerminal(root.ErrOrStderr()),
		OptOut:         env.UpdateNotifierOptOut(),
	})

	updCtx, updCancel := context.WithCancel(context.Background())
	defer updCancel()
	resultCh := make(chan updater.Result, 1)
	if preSkip == updater.SkipNone {
		// Run even in JSON mode so the cache refreshes; the gate below decides on printing.
		go func() {
			resultCh <- updater.Check(updCtx, version, updater.CacheDir())
		}()
	}

	runErr := root.Execute()

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
		if skip == updater.SkipNone {
			waitAndNotify(root.ErrOrStderr(), resultCh, updCancel, updateNoticeWait)
		}
		os.Exit(exitCode(runErr))
	}

	if skip != updater.SkipNone {
		return
	}
	waitAndNotify(root.ErrOrStderr(), resultCh, updCancel, updateNoticeWait)
}

// updateNoticeWait caps how long Execute blocks for the update check. 1s matches the spec.
const updateNoticeWait = 1 * time.Second

func waitAndNotify(w io.Writer, resultCh <-chan updater.Result, updCancel context.CancelFunc, wait time.Duration) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case r := <-resultCh:
		_ = updater.MaybeNotify(w, r, ui.IsTTY(w))
	case <-timer.C:
		updCancel()
	}
}

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

	b.WriteString(ui.Heading("USAGE", tty))
	b.WriteByte('\n')
	b.WriteString("  ")
	b.WriteString(c.UseLine())
	if c.HasAvailableSubCommands() {
		b.WriteString(" <command> [flags]")
	}
	b.WriteString("\n\n")

	if c.HasAvailableSubCommands() {
		writeGroupedCommands(&b, c, tty)
	}

	flags := c.LocalFlags()
	if c.HasAvailableInheritedFlags() {
		flags.AddFlagSet(c.InheritedFlags())
	}
	if flags.HasAvailableFlags() {
		b.WriteString(ui.Heading("FLAGS", tty))
		b.WriteByte('\n')
		b.WriteString(indent(flags.FlagUsages(), "  "))
		b.WriteByte('\n')
	}

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

func writeGroupedCommands(b *strings.Builder, c *cobra.Command, tty bool) {
	type bucket struct {
		title string
		cmds  []*cobra.Command
	}

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
