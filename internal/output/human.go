package output

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/emailable/emailable-cli/internal/api"
	"github.com/emailable/emailable-cli/internal/ui"
)

// Human renders for a terminal. It type-switches on the value:
//   - *api.VerifyResult or api.VerifyResult: labeled key/value block,
//     state colored (green/red/yellow/gray) when stdout is a TTY.
//   - *api.BatchSubmit: "Batch submitted: <id>" plus a hint.
//   - *api.BatchStatus: "card-like" status block with id + counter.
//   - []api.VerifyResult: compact table with columns EMAIL, STATE, SCORE, REASON.
//   - *AccountView: three-line summary (owner email, credit balance, API host).
//   - any other value: JSON fallback via the JSON formatter.
//
// Colors are emitted only when w refers to a TTY. Detection is best-effort
// using golang.org/x/term — when in doubt, plain text is printed. We bypass
// lipgloss's default renderer (which probes stderr/stdout globally) and
// gate each render on the actual writer we're printing to.
type Human struct {
	W io.Writer
	// Quiet, when true, suppresses Success / Hint / Notice — the "chrome"
	// methods that print confirmations, follow-up tips, and in-band notices.
	// Error rendering is unaffected (errors go through a separate code path).
	// Print and the typed Print* methods are unaffected too: a user asking
	// for quiet output of a batch table still wants the table.
	Quiet bool
}

// Success prints a one-line confirmation styled as `✓ <msg>` — green ✓,
// bold message. Use for "Batch submitted: …", "Logged in", "Saved N
// results …", and any other terminal success line. Keeps the symbol
// vocabulary consistent across commands.
//
// No-ops (returns nil without writing) when Quiet is true.
func (h *Human) Success(msg string) error {
	if h.Quiet {
		return nil
	}
	stf := styler(h.W)
	check := stf(lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)).Render("✓")
	body := stf(lipgloss.NewStyle().Bold(true)).Render(msg)
	_, err := fmt.Fprintf(h.W, "%s %s\n", check, body)
	return err
}

// Notice prints a single dimmed informational line — no leading blank, no
// glyph. Use it for in-band status messages like "Refreshed access token."
// or the device-flow "First copy your one-time code: …" prompt where a
// Success/Hint would feel too celebratory or too separated. Backtick
// segments render a shade lighter so commands/codes stand out, matching
// the Hint convention.
func (h *Human) Notice(msg string) error {
	if h.Quiet {
		return nil
	}
	stf := styler(h.W)
	dim := stf(lipgloss.NewStyle().Foreground(lipgloss.Color("241")))
	code := stf(lipgloss.NewStyle().Foreground(lipgloss.Color("250")))

	var b strings.Builder
	for i, part := range strings.Split(msg, "`") {
		if i%2 == 0 {
			b.WriteString(dim.Render(part))
		} else {
			b.WriteString(code.Render(part))
		}
	}
	_, err := fmt.Fprintln(h.W, b.String())
	return err
}

// Hint prints a dimmed follow-up line preceded by a blank line so it
// visually separates from the primary success/result output above it.
// Use for "Run `emailable …` to do X" tips and download-URL pointers.
//
// Text wrapped in backticks renders a shade lighter than the surrounding
// dim text so commands and flags stand out as copyable tokens without
// jumping fully to the default foreground. The backticks themselves are
// stripped from the output. On non-TTY writers the styling collapses to
// plain text with backticks removed.
func (h *Human) Hint(msg string) error {
	if h.Quiet {
		return nil
	}
	stf := styler(h.W)
	dim := stf(lipgloss.NewStyle().Foreground(lipgloss.Color("241")))
	code := stf(lipgloss.NewStyle().Foreground(lipgloss.Color("250")))

	var b strings.Builder
	for i, part := range strings.Split(msg, "`") {
		if i%2 == 0 {
			b.WriteString(dim.Render(part))
		} else {
			b.WriteString(code.Render(part))
		}
	}
	_, err := fmt.Fprintf(h.W, "\n%s\n", b.String())
	return err
}

// Print dispatches on the runtime type of v.
func (h *Human) Print(v any) error {
	switch x := v.(type) {
	case *api.VerifyResult:
		return h.PrintVerifyResult(x)
	case api.VerifyResult:
		return h.PrintVerifyResult(&x)
	case *api.BatchSubmit:
		if err := h.Success(fmt.Sprintf("Batch submitted: %s", x.ID)); err != nil {
			return err
		}
		return h.Hint(fmt.Sprintf("Run `emailable batch get %s` to check progress.", x.ID))
	case api.BatchSubmit:
		return h.Print(&x)
	case *api.BatchStatus:
		if len(x.Emails) > 0 {
			return h.PrintBatchResults(x.Emails)
		}
		if x.DownloadFile != "" {
			if err := h.Success("Batch complete"); err != nil {
				return err
			}
			return h.Hint(fmt.Sprintf("Too many results to display inline — download from:\n  `%s`", x.DownloadFile))
		}
		return h.PrintBatchStatus(x)
	case api.BatchStatus:
		return h.Print(&x)
	case []api.VerifyResult:
		return h.PrintBatchResults(x)
	case *AccountView:
		return h.PrintAccountView(x)
	case AccountView:
		return h.PrintAccountView(&x)
	default:
		return (&JSON{W: h.W}).Print(v)
	}
}

// State palette. Hex values mirror the Emailable dashboard's brand colors:
// coral-pink red for undeliverable, powder-blue for unknown. Lipgloss
// auto-degrades to the nearest 256-color value on terminals that don't
// support truecolor.
const (
	colorDeliverable   = lipgloss.Color("42")      // green
	colorUndeliverable = lipgloss.Color("#EE6F84") // dashboard coral-pink
	colorRisky         = lipgloss.Color("214")     // yellow/orange
	colorUnknown       = lipgloss.Color("#7EB7DE") // dashboard powder-blue
)

// stateColor returns a lipgloss color for a verification state value.
// 0 means "no color".
func stateColor(state string) lipgloss.Color {
	switch state {
	case "deliverable":
		return colorDeliverable
	case "undeliverable":
		return colorUndeliverable
	case "risky":
		return colorRisky
	case "unknown":
		return colorUnknown
	default:
		return lipgloss.Color("")
	}
}

// stateGlyph returns a small leading glyph that matches the state's
// semantic: ✓ deliverable, ✗ undeliverable, ! risky, ? unknown.
func stateGlyph(state string) string {
	switch state {
	case "deliverable":
		return "✓"
	case "undeliverable":
		return "✗"
	case "risky":
		return "!"
	case "unknown":
		return "?"
	default:
		return " "
	}
}

// hyperlink wraps text in an OSC 8 escape sequence so supporting terminals
// (iTerm2, kitty, alacritty, recent gnome-terminal, etc) render it as a
// clickable link. Older terminals ignore the escape and just print text.
// When enabled is false, returns text unchanged.
func hyperlink(url, text string, enabled bool) string {
	if !enabled || url == "" {
		return text
	}
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// isTTY reports whether w is a terminal AND ANSI styling is enabled. Delegates
// to ui.IsTTY so the NO_COLOR env var suppresses styling here too.
func isTTY(w io.Writer) bool {
	return ui.IsTTY(w)
}

// styler returns a lipgloss style configured for the writer's color
// capability. When w isn't a TTY all styles render as plain text.
func styler(w io.Writer) func(lipgloss.Style) lipgloss.Style {
	tty := isTTY(w)
	return func(s lipgloss.Style) lipgloss.Style {
		if !tty {
			// Strip styling by returning a fresh empty style. Lipgloss
			// renders an empty Style as the raw input.
			return lipgloss.NewStyle()
		}
		return s
	}
}

// StylerFor is the exported form of styler so callers outside this
// package (one-off styled lines in cmd/) can render with the same
// TTY-gated approach used by the formatters here.
func StylerFor(w io.Writer) func(lipgloss.Style) lipgloss.Style {
	return styler(w)
}

// yesNo converts a bool to "Yes" or "No" — capitalized to match the
// dashboard's attribute table styling.
func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

// titleFirst capitalizes the first byte of s. Used for short ASCII tokens
// returned in lowercase by the API (state, gender). Not Unicode-aware on
// purpose: these values are ASCII per the API.
func titleFirst(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// humanizeState capitalizes the first letter of an API state value.
// API returns lowercase like "deliverable"; humans see "Deliverable".
func humanizeState(s string) string {
	return titleFirst(s)
}

// humanizeReason maps snake_case API reason codes to their canonical
// Emailable-app display labels (Accepted Email, Low Deliverability, etc).
// Returns the input unchanged for unknown codes so we don't lose data.
func humanizeReason(r string) string {
	switch r {
	case "accepted_email":
		return "Accepted Email"
	case "invalid_domain":
		return "Invalid Domain"
	case "invalid_email":
		return "Invalid Email"
	case "invalid_smtp":
		return "Invalid SMTP"
	case "low_deliverability":
		return "Low Deliverability"
	case "low_quality":
		return "Low Quality"
	case "no_connect":
		return "No Connect"
	case "rejected_email":
		return "Rejected Email"
	case "timeout":
		return "Timeout"
	case "unavailable_smtp":
		return "Unavailable SMTP"
	case "unexpected_error":
		return "Unexpected Error"
	default:
		return r
	}
}

// scoreDisplay returns the user-facing score string. For "unknown" state
// the numeric score isn't meaningful (the API reports 0 alongside the
// unknown verdict), so we render an em-dash instead. Used everywhere
// score is shown: single-verify header badge, single-verify Score row,
// batch table SCORE column.
func scoreDisplay(score int, state string) string {
	if state == "unknown" {
		return "—"
	}
	return strconv.Itoa(score)
}

// scoreBadgeBG returns the lipgloss background color for the score "pill"
// shown next to the email in the verify result header. Bands mirror the
// dashboard: green for high deliverability, yellow for risky, coral-pink
// for the known-bad zero score, powder-blue when the state is "unknown".
// Reuses the same palette constants as stateColor so the badge and the
// State row read as a set.
func scoreBadgeBG(score int, state string) lipgloss.Color {
	if state == "unknown" {
		return colorUnknown
	}
	switch {
	case score >= 80:
		return colorDeliverable
	case score >= 1:
		return colorRisky
	default:
		return colorUndeliverable
	}
}

// PrintVerifyResult renders a single real-time verify result using the
// same section structure as the Emailable web dashboard's Email Verifier:
// a header line with the email + a colored score badge, then "General",
// "Attributes", and "Mail Server" sections. Section titles are bold;
// labels are dimmed; State and the score badge carry semantic color.
//
// The human view intentionally matches the dashboard — `user` and
// `duration` are hidden here (they remain in `--json` for scripts).
// Optional rows are skipped when empty; empty sections are skipped
// entirely.
func (h *Human) PrintVerifyResult(r *api.VerifyResult) error {
	stf := styler(h.W)
	labelStyle := stf(lipgloss.NewStyle().Foreground(lipgloss.Color("241")))
	sectionStyle := stf(lipgloss.NewStyle().Bold(true))
	stateStyle := stf(lipgloss.NewStyle().Foreground(stateColor(r.State)).Bold(true))
	glyphStyle := stf(lipgloss.NewStyle().Foreground(stateColor(r.State)).Bold(true))
	emailStyle := stf(lipgloss.NewStyle().Bold(true))
	badgeStyle := stf(lipgloss.NewStyle().
		Background(scoreBadgeBG(r.Score, r.State)).
		Foreground(lipgloss.Color("0")).
		Bold(true).
		Padding(0, 1))
	scoreText := scoreDisplay(r.Score, r.State)

	type row struct {
		label string
		value string
	}
	type section struct {
		title string
		rows  []row
	}

	// General — identity + the headline verification verdict.
	general := section{title: "General"}
	fullName := r.FullName
	if fullName == "" {
		fullName = strings.TrimSpace(r.FirstName + " " + r.LastName)
	}
	if fullName != "" {
		general.rows = append(general.rows, row{"Full Name", fullName})
	}
	if r.Gender != "" {
		general.rows = append(general.rows, row{"Gender", titleFirst(r.Gender)})
	}
	if r.State != "" {
		// State value is rendered as a colored badge containing the state's
		// icon (✓ / ! / ✗ / ?), followed by the humanized state name in the
		// same color. Matches the circled-icon style of the dashboard.
		stateBadgeStyle := stf(lipgloss.NewStyle().
			Background(stateColor(r.State)).
			Foreground(lipgloss.Color("0")).
			Bold(true).
			Padding(0, 1))
		badge := stateBadgeStyle.Render(stateGlyph(r.State))
		general.rows = append(general.rows, row{
			"State",
			badge + " " + stateStyle.Render(humanizeState(r.State)),
		})
	}
	if r.Reason != "" {
		general.rows = append(general.rows, row{"Reason", humanizeReason(r.Reason)})
	}
	if r.Domain != "" {
		// Domain renders as an OSC 8 hyperlink to https://<domain> when stdout
		// is a TTY, with cyan + underline styling so it looks like a link in
		// terminals that don't honor OSC 8.
		linkStyle := stf(lipgloss.NewStyle().Foreground(lipgloss.Color("69")).Underline(true))
		domainText := linkStyle.Render(r.Domain)
		general.rows = append(general.rows, row{
			"Domain",
			hyperlink("https://"+r.Domain, domainText, isTTY(h.W)),
		})
	}
	if r.DidYouMean != "" {
		general.rows = append(general.rows, row{"Did you mean", r.DidYouMean})
	}

	// Attributes — boolean denotations + tag.
	attrs := section{title: "Attributes", rows: []row{
		{"Free", yesNo(r.Free)},
		{"Role", yesNo(r.Role)},
		{"Disposable", yesNo(r.Disposable)},
		{"Accept-All", yesNo(r.AcceptAll)},
	}}
	if r.Tag != "" {
		attrs.rows = append(attrs.rows, row{"Tag", r.Tag})
	}
	attrs.rows = append(attrs.rows,
		row{"Mailbox Full", yesNo(r.MailboxFull)},
		row{"No Reply", yesNo(r.NoReply)},
	)

	// Mail Server — DNS / SMTP-side facts.
	mail := section{title: "Mail Server"}
	if r.SMTPProvider != "" {
		mail.rows = append(mail.rows, row{"SMTP Provider", r.SMTPProvider})
	}
	if r.MXRecord != "" {
		mail.rows = append(mail.rows, row{"MX Record", r.MXRecord})
	}

	sections := []section{general, attrs, mail}

	// Compute label width across all rendered rows for clean alignment.
	width := 0
	for _, s := range sections {
		for _, rr := range s.rows {
			if len(rr.label) > width {
				width = len(rr.label)
			}
		}
	}

	// Header: "<glyph> <email>  [score]"
	if r.Email != "" {
		glyph := stateGlyph(r.State)
		header := emailStyle.Render(r.Email)
		if glyph != " " {
			header = glyphStyle.Render(glyph) + " " + header
		}
		badge := badgeStyle.Render(scoreText)
		if _, err := fmt.Fprintf(h.W, "%s  %s\n", header, badge); err != nil {
			return err
		}
	}

	for _, s := range sections {
		if len(s.rows) == 0 {
			continue
		}
		if _, err := fmt.Fprintln(h.W); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(h.W, sectionStyle.Render(s.title)); err != nil {
			return err
		}
		for _, rr := range s.rows {
			pad := width - len(rr.label) + 2
			if _, err := fmt.Fprintf(h.W, "  %s%s%s\n",
				labelStyle.Render(rr.label),
				strings.Repeat(" ", pad),
				rr.value,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

// PrintBatchStatus renders the in-progress batch status as a compact
// "status card": a spinner-like glyph + bold ID on line 1, then a dimmed
// "processed/total (pct%)" counter indented on line 2. When the payload
// carries no progress counters (queued batch or partial=true response
// without TotalCounts) the counter is replaced with "(starting...)".
func (h *Human) PrintBatchStatus(s *api.BatchStatus) error {
	stf := styler(h.W)
	idStyle := stf(lipgloss.NewStyle().Bold(true))
	dim := stf(lipgloss.NewStyle().Foreground(lipgloss.Color("241")))
	glyphStyle := stf(lipgloss.NewStyle().Foreground(lipgloss.Color("69")))
	doneStyle := stf(lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true))

	complete := s.IsComplete()
	if s.ID != "" {
		var line string
		if complete {
			line = doneStyle.Render("✓") + " Batch " + idStyle.Render(s.ID) + dim.Render(" — complete")
		} else {
			line = glyphStyle.Render("⠋") + " Batch " + idStyle.Render(s.ID)
		}
		if _, err := fmt.Fprintln(h.W, line); err != nil {
			return err
		}
	}
	processed, total, ok := s.Progress()
	if !ok {
		_, err := fmt.Fprintf(h.W, "  %s\n", dim.Render("(starting...)"))
		return err
	}
	pct := int((float64(processed) / float64(total)) * 100)
	_, err := fmt.Fprintf(h.W, "  %s\n", dim.Render(fmt.Sprintf("%d/%d (%d%%)", processed, total, pct)))
	return err
}

// PrintBatchSummary renders a one-line outcome summary headed by per-state
// counts (deliverable / undeliverable / risky / unknown) joined with commas.
//
// The leading glyph and verb reflect whether the batch has finished:
//   - complete:  "✓ Verified N emails: …"
//   - in-flight: "⋯ Partial results (M of N processed): …"
//
// Partial detection: when the batch is not yet complete (per BatchStatus.
// IsComplete) we treat the payload as a partial snapshot — this covers the
// partial=true response shape, which embeds progress in TotalCounts and
// includes whatever rows are ready so far.
func (h *Human) PrintBatchSummary(s *api.BatchStatus) error {
	stf := styler(h.W)

	counts := make(map[string]int, 4)
	for _, e := range s.Emails {
		counts[e.State]++
	}

	var parts []string
	for _, state := range []string{"deliverable", "undeliverable", "risky", "unknown"} {
		n := counts[state]
		if n == 0 {
			continue
		}
		label := fmt.Sprintf("%d %s", n, humanizeState(state))
		c := stateColor(state)
		if c != lipgloss.Color("") {
			label = stf(lipgloss.NewStyle().Foreground(c)).Render(label)
		}
		parts = append(parts, label)
	}

	tail := ""
	if len(parts) > 0 {
		tail = ": " + strings.Join(parts, ", ")
	}

	if s.IsComplete() {
		check := stf(lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)).Render("✓")
		_, err := fmt.Fprintf(h.W, "%s Verified %d emails%s\n", check, len(s.Emails), tail)
		return err
	}

	// Partial / in-flight rendering. The blue-ish glyph matches the
	// PrintBatchStatus card so users see a consistent "still working"
	// signal across `batch get` (no flag), `batch get --partial`, and the
	// queued-spinner phase of `--wait`.
	glyph := stf(lipgloss.NewStyle().Foreground(lipgloss.Color("69")).Bold(true)).Render("⋯")
	processed, total, hasProgress := s.Progress()
	progressNote := ""
	if hasProgress {
		progressNote = fmt.Sprintf(" (%d of %d processed)", processed, total)
	}
	_, err := fmt.Fprintf(h.W, "%s Partial results%s%s\n", glyph, progressNote, tail)
	return err
}

// PrintBatchResults renders the per-email results table. Styling mirrors
// the single-verify card aesthetic: SCORE is a colored background pill,
// STATE pairs the state icon pill with the bold colored state name in a
// single cell (just like the single-verify State row), email is bold,
// header row is bold cyan, separator is dimmed. Column widths are computed
// from rendered cells via lipgloss.Width so ANSI codes don't skew alignment.
//
// Column order: EMAIL, SCORE, STATE, REASON.
func (h *Human) PrintBatchResults(results []api.VerifyResult) error {
	stf := styler(h.W)
	headStyle := stf(lipgloss.NewStyle().Foreground(lipgloss.Color("241")))
	emailStyle := stf(lipgloss.NewStyle().Bold(true))
	dimStyle := stf(lipgloss.NewStyle().Foreground(lipgloss.Color("241")))

	headers := []string{"EMAIL", "SCORE", "STATE", "REASON"}
	headerCells := make([]string, len(headers))
	for i, hd := range headers {
		headerCells[i] = headStyle.Render(hd)
	}

	rows := make([][]string, 0, len(results))
	for _, r := range results {
		stateColored := stf(lipgloss.NewStyle().Foreground(stateColor(r.State)).Bold(true)).
			Render(humanizeState(r.State))
		stateBadge := stf(lipgloss.NewStyle().
			Background(stateColor(r.State)).
			Foreground(lipgloss.Color("0")).
			Bold(true).
			Padding(0, 1)).
			Render(stateGlyph(r.State))

		// Build a fixed-width pill body so every score renders as the same
		// 5-column colored box: 1 gutter + 3 right-aligned content cols +
		// 1 gutter. We bake the gutters into the rendered string (instead
		// of using lipgloss Padding) so the entire box — including the
		// leading whitespace inside the content area — is wrapped in one
		// styled span and the background extends across the full width.
		scoreText := scoreDisplay(r.Score, r.State)
		if pad := 3 - lipgloss.Width(scoreText); pad > 0 {
			scoreText = strings.Repeat(" ", pad) + scoreText
		}
		scorePill := stf(lipgloss.NewStyle().
			Background(scoreBadgeBG(r.Score, r.State)).
			Foreground(lipgloss.Color("0")).
			Bold(true)).
			Render(" " + scoreText + " ")

		rows = append(rows, []string{
			emailStyle.Render(r.Email),
			scorePill,
			stateBadge + " " + stateColored,
			humanizeReason(r.Reason),
		})
	}

	// Compute column widths from rendered cells (lipgloss.Width strips ANSI
	// and counts visual columns, so the pill backgrounds widen the glyph and
	// score columns correctly).
	widths := make([]int, len(headers))
	for i, hd := range headerCells {
		widths[i] = lipgloss.Width(hd)
	}
	for _, row := range rows {
		for i, c := range row {
			if w := lipgloss.Width(c); w > widths[i] {
				widths[i] = w
			}
		}
	}

	padSpaces := func(s string, w int) string {
		n := w - lipgloss.Width(s)
		if n <= 0 {
			return ""
		}
		return strings.Repeat(" ", n)
	}

	var b strings.Builder

	// Header
	for i, c := range headerCells {
		b.WriteString(c)
		b.WriteString(padSpaces(c, widths[i]))
		if i < len(headerCells)-1 {
			b.WriteString("  ")
		}
	}
	b.WriteString("\n")

	// Dimmed separator using a box-drawing horizontal line.
	for i, w := range widths {
		b.WriteString(dimStyle.Render(strings.Repeat("─", w)))
		if i < len(widths)-1 {
			b.WriteString("  ")
		}
	}
	b.WriteString("\n")

	// Body rows
	for _, row := range rows {
		for i, c := range row {
			b.WriteString(c)
			b.WriteString(padSpaces(c, widths[i]))
			if i < len(row)-1 {
				b.WriteString("  ")
			}
		}
		b.WriteString("\n")
	}

	_, err := h.W.Write([]byte(b.String()))
	return err
}

// formatThousands renders an integer with comma thousands separators.
func formatThousands(n int) string {
	s := strconv.Itoa(n)
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	if len(s) <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
		if len(s) > pre {
			b.WriteString(",")
		}
	}
	for i := pre; i < len(s); i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < len(s) {
			b.WriteString(",")
		}
	}
	out := b.String()
	if neg {
		return "-" + out
	}
	return out
}

// PrintAccountView renders the three-line account summary. Labels are dimmed;
// the email and credits are bold so the eye lands on the data first. The host
// line is dimmed since it's metadata used for confirming which backend the
// CLI is talking to rather than primary information.
func (h *Human) PrintAccountView(v *AccountView) error {
	stf := styler(h.W)
	label := stf(lipgloss.NewStyle().Foreground(lipgloss.Color("241")))
	value := stf(lipgloss.NewStyle().Bold(true))

	if _, err := fmt.Fprintf(h.W, "%s  %s\n", label.Render("Account:"), value.Render(v.OwnerEmail)); err != nil {
		return err
	}
	_, err := fmt.Fprintf(h.W, "%s  %s\n", label.Render("Credits:"), value.Render(formatThousands(v.AvailableCredits)))

	return err
}
