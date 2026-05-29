package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/emailable/emailable-cli/internal/output"
	"github.com/emailable/emailable-cli/internal/skill"
	"github.com/emailable/emailable-cli/internal/ui"
	"github.com/spf13/cobra"
)

// newSkillCmd is the `emailable skill` command group. Bare invocation
// on a TTY launches a picker; otherwise it shows help.
func newSkillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage the agent skill for the Emailable CLI",
		Long: "Manage the agent skill the Emailable CLI ships with. Installing " +
			"writes SKILL.md to ~/.agents/skills/emailable and symlinks every " +
			"detected agent (Claude Code, OpenCode, Codex) into that canonical " +
			"copy, so one re-install picks up new releases everywhere.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if jsonOutput || quietMode || !terminalsInteractive(cmd) {
				return cmd.Help()
			}
			return runSkillWizard(cmd)
		},
	}
	cmd.AddCommand(newSkillInstallCmd(), newSkillPrintCmd(), newSkillTargetsCmd())
	return cmd
}

func runSkillWizard(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	stf := output.StylerFor(out)
	title := stf(lipgloss.NewStyle().Bold(true).Foreground(ui.BrandPurple))

	fmt.Fprintln(out)
	fmt.Fprintln(out, "  "+title.Render("Emailable Skill Installation"))
	fmt.Fprintln(out)

	targets := skill.Targets()
	choices := make([]ui.Choice, 0, len(targets)+1)
	for _, t := range targets {
		choices = append(choices, ui.Choice{
			Label: fmt.Sprintf("%s (%s)", t.Name, filepath.Join(t.Dir, skill.FileName)),
		})
	}
	choices = append(choices, ui.Choice{Label: "Other (custom path)"})

	idx, ok, err := ui.Select(os.Stdin, out, "Where would you like to install the Emailable skill?", choices)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if idx == len(targets) {
		return runSkillWizardCustom(cmd)
	}
	res, err := skill.InstallOne(targets[idx])
	if err != nil {
		return err
	}
	return renderInstallResult(cmd, res)
}

func runSkillWizardCustom(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	raw, ok, err := ui.PromptWithPlaceholder(os.Stdin, out, "Enter custom path", "/path/to/skills/emailable/SKILL.md", false)
	if err != nil {
		return err
	}
	if !ok || raw == "" {
		return nil
	}

	normalized := skill.NormalizeCustomPath(raw)
	abs, err := skill.Expand(normalized)
	if err != nil {
		return err
	}
	if proceed, err := confirmOverwriteIfExists(out, abs); err != nil || !proceed {
		return err
	}

	path, err := skill.InstallToFile(normalized)
	if err != nil {
		return err
	}
	return renderInstallResult(cmd, skill.Result{SkillPath: path})
}

func confirmOverwriteIfExists(out io.Writer, path string) (bool, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return true, nil
	} else if err != nil {
		return false, err
	}
	return ui.Confirm(os.Stdin, out, fmt.Sprintf("%s already exists. Overwrite?", path))
}

var (
	skillInstallDir    string
	skillInstallTarget string
	skillInstallAll    bool
)

func newSkillInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "install",
		Short:        "Install the skill for one or more agents",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Long: "Install the skill. With no flags, writes the canonical copy at " +
			"~/.agents/skills/emailable and symlinks every detected agent's " +
			"skills directory back to it. Use --target to install for one " +
			"specific agent, --all to symlink every known global agent " +
			"location (whether detected or not), or --dir to write directly " +
			"to a custom directory (no symlinks).\n\n" +
			"Run `emailable skill targets` for the full list of --target IDs.",
		Example: `  # Detect installed agents and link them all into ~/.agents/skills/emailable
  emailable skill install

  # Install for one specific agent
  emailable skill install --target claude-global
  emailable skill install --target opencode-project

  # Symlink every known global agent location (even if not detected)
  emailable skill install --all

  # Install into a custom directory (no symlinks)
  emailable skill install --dir ./vendor/skills/emailable`,
		RunE: runSkillInstallE,
	}
	cmd.Flags().StringVar(&skillInstallDir, "dir", "", "Directory to install SKILL.md into (skips the canonical copy and symlinks)")
	cmd.Flags().StringVar(&skillInstallTarget, "target", "", "Install for a single agent by ID (see `emailable skill targets`)")
	cmd.Flags().BoolVar(&skillInstallAll, "all", false, "Symlink every known global agent location, whether detected or not")
	return cmd
}

func newSkillPrintCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "print",
		Short:        "Print SKILL.md to stdout",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example: `  # Pipe to a custom location
  emailable skill print > ~/path/to/SKILL.md`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprint(cmd.OutOrStdout(), skill.Content())
			return err
		},
	}
}

func newSkillTargetsCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "targets",
		Short:        "List known --target IDs and their paths",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ts := skill.Targets()
			if jsonOutput {
				rows := make([]map[string]any, 0, len(ts))
				for _, t := range ts {
					row := map[string]any{"id": t.ID, "name": t.Name, "dir": t.Dir, "scope": scopeOf(t)}
					if t.Detect != nil {
						row["detected"] = t.Detect()
					}
					rows = append(rows, row)
				}
				return (&output.JSON{W: cmd.OutOrStdout()}).Print(map[string]any{"targets": rows})
			}
			printTargetsTable(cmd.OutOrStdout(), ts)
			return nil
		},
	}
}

func runSkillInstallE(cmd *cobra.Command, _ []string) error {
	if err := validateInstallFlags(); err != nil {
		return err
	}

	if skillInstallDir != "" {
		path, err := skill.InstallToDir(skillInstallDir)
		if err != nil {
			return err
		}
		return renderInstallResult(cmd, skill.Result{SkillPath: path})
	}

	var (
		res skill.Result
		err error
	)
	switch {
	case skillInstallTarget != "":
		loc, ok := skill.LookupTarget(skillInstallTarget)
		if !ok {
			return NewInvalidInput(fmt.Sprintf("unknown --target %q; run `emailable skill targets` for the list", skillInstallTarget))
		}
		res, err = skill.InstallOne(loc)
	case skillInstallAll:
		res, err = skill.InstallAll()
	default:
		res, err = skill.InstallDetected()
	}
	if err != nil {
		return err
	}
	return renderInstallResult(cmd, res)
}

func validateInstallFlags() error {
	set := 0
	if skillInstallDir != "" {
		set++
	}
	if skillInstallTarget != "" {
		set++
	}
	if skillInstallAll {
		set++
	}
	if set > 1 {
		return NewInvalidInput("--dir, --target, and --all are mutually exclusive")
	}
	return nil
}

func renderInstallResult(cmd *cobra.Command, res skill.Result) error {
	if jsonOutput {
		links := make([]map[string]any, 0, len(res.Links))
		for _, l := range res.Links {
			row := map[string]any{
				"target": l.Target.ID,
				"name":   l.Target.Name,
				"path":   l.Path,
			}
			if l.Notice != "" {
				row["notice"] = l.Notice
			}
			links = append(links, row)
		}
		return (&output.JSON{W: cmd.OutOrStdout()}).Print(map[string]any{
			"skill_path": res.SkillPath,
			"links":      links,
		})
	}
	h := &output.Human{W: cmd.OutOrStdout(), Quiet: quietMode}
	if err := h.Success(fmt.Sprintf("Skill installed to %s", res.SkillPath)); err != nil {
		return err
	}
	if len(res.Links) == 0 {
		return h.Hint("Run `emailable skill targets` to see agent-specific install paths.")
	}
	printLinks(cmd.OutOrStdout(), res.Links)
	return nil
}

func printLinks(w io.Writer, links []skill.LinkResult) {
	stf := output.StylerFor(w)
	dim := stf(lipgloss.NewStyle().Faint(true))
	bullet := stf(lipgloss.NewStyle().Foreground(lipgloss.Color("250")))
	fmt.Fprintln(w)
	fmt.Fprintln(w, dim.Render("Linked into agent locations:"))
	for _, l := range links {
		line := fmt.Sprintf("  %s %s %s %s", bullet.Render("•"), l.Target.Name, dim.Render("→"), l.Path)
		if l.Notice != "" {
			line += "  " + dim.Render("("+l.Notice+")")
		}
		fmt.Fprintln(w, line)
	}
}

func printTargetsTable(w io.Writer, ts []skill.Location) {
	type row struct{ id, scope, dir, detected string }
	rows := make([]row, 0, len(ts))
	for _, t := range ts {
		r := row{id: t.ID, scope: scopeOf(t), dir: t.Dir}
		if t.Detect != nil {
			if t.Detect() {
				r.detected = "yes"
			} else {
				r.detected = "no"
			}
		}
		rows = append(rows, r)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if (rows[i].scope == "global") != (rows[j].scope == "global") {
			return rows[i].scope == "global"
		}
		return rows[i].id < rows[j].id
	})
	pad := 0
	for _, r := range rows {
		if n := len(r.id); n > pad {
			pad = n
		}
	}
	stf := output.StylerFor(w)
	head := stf(lipgloss.NewStyle().Foreground(lipgloss.Color("241")))
	dim := stf(lipgloss.NewStyle().Faint(true))
	fmt.Fprintf(w, "%s  %s  %s  %s\n",
		head.Render(rpad("ID", pad)),
		head.Render(rpad("SCOPE", 7)),
		head.Render(rpad("DETECTED", 8)),
		head.Render("DIR"),
	)
	for _, r := range rows {
		fmt.Fprintf(w, "%s  %s  %s  %s\n",
			rpad(r.id, pad),
			rpad(r.scope, 7),
			dim.Render(rpad(orDash(r.detected), 8)),
			r.dir,
		)
	}
}

func scopeOf(t skill.Location) string {
	if t.Global {
		return "global"
	}
	return "project"
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func rpad(s string, w int) string {
	if n := len(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}
