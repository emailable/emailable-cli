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

// Human renders for a terminal, type-switching on the value to produce
// labeled blocks, status cards, and tables. Colors gate on the actual writer
// rather than lipgloss's default renderer, which probes stderr/stdout globally.
type Human struct {
	W io.Writer
	// Quiet suppresses Success/Hint/Notice; typed Print* methods still run.
	Quiet bool
}

// Success prints a bold green check mark followed by msg.
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

// Notice prints msg in dim text, rendering backtick-delimited spans in a lighter color.
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

// Hint prints a dimmed follow-up tip, preceded by a blank line.
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

// Print renders v to h.W, dispatching on v's runtime type.
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

// Hex values match the dashboard brand palette; lipgloss degrades to nearest
// 256-color where truecolor is unsupported.
const (
	colorDeliverable   = lipgloss.Color("42")
	colorUndeliverable = lipgloss.Color("#EE6F84")
	colorRisky         = lipgloss.Color("214")
	colorUnknown       = lipgloss.Color("#7EB7DE")
)

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

// hyperlink wraps text in an OSC 8 escape; unsupporting terminals ignore it.
func hyperlink(url, text string, enabled bool) string {
	if !enabled || url == "" {
		return text
	}
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

func isTTY(w io.Writer) bool {
	return ui.IsTTY(w)
}

func styler(w io.Writer) func(lipgloss.Style) lipgloss.Style {
	tty := isTTY(w)
	return func(s lipgloss.Style) lipgloss.Style {
		if !tty {
			return lipgloss.NewStyle()
		}
		return s
	}
}

// StylerFor returns a style transformer that strips styles when w is not a TTY.
func StylerFor(w io.Writer) func(lipgloss.Style) lipgloss.Style {
	return styler(w)
}

func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

// titleFirst capitalizes the first byte of s; intentionally not Unicode-aware
// since API tokens (state, gender) are ASCII.
func titleFirst(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func humanizeState(s string) string {
	return titleFirst(s)
}

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

// scoreDisplay renders an em-dash for "unknown" — the API reports 0, which
// would be misleading.
func scoreDisplay(score int, state string) string {
	if state == "unknown" {
		return "—"
	}
	return strconv.Itoa(score)
}

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

// PrintVerifyResult renders a single verify result. The `user` and `duration`
// fields are intentionally omitted here; JSON output retains them.
// PrintVerifyResult renders r as a labeled card with state, score, and attribute sections.
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

	mail := section{title: "Mail Server"}
	if r.SMTPProvider != "" {
		mail.rows = append(mail.rows, row{"SMTP Provider", r.SMTPProvider})
	}
	if r.MXRecord != "" {
		mail.rows = append(mail.rows, row{"MX Record", r.MXRecord})
	}

	sections := []section{general, attrs, mail}

	width := 0
	for _, s := range sections {
		for _, rr := range s.rows {
			if len(rr.label) > width {
				width = len(rr.label)
			}
		}
	}

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

// PrintBatchStatus renders the ID and progress of an in-flight batch.
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

// PrintBatchSummary renders a one-line state-breakdown summary for a batch.
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

	glyph := stf(lipgloss.NewStyle().Foreground(lipgloss.Color("69")).Bold(true)).Render("⋯")
	processed, total, hasProgress := s.Progress()
	progressNote := ""
	if hasProgress {
		progressNote = fmt.Sprintf(" (%d of %d processed)", processed, total)
	}
	_, err := fmt.Fprintf(h.W, "%s Partial results%s%s\n", glyph, progressNote, tail)
	return err
}

// PrintBatchResults renders results as a color-coded table with email, score, state, and reason columns.
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

	for i, c := range headerCells {
		b.WriteString(c)
		b.WriteString(padSpaces(c, widths[i]))
		if i < len(headerCells)-1 {
			b.WriteString("  ")
		}
	}
	b.WriteString("\n")

	for i, w := range widths {
		b.WriteString(dimStyle.Render(strings.Repeat("─", w)))
		if i < len(widths)-1 {
			b.WriteString("  ")
		}
	}
	b.WriteString("\n")

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

// PrintAccountView renders the account owner email and available credit balance.
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
