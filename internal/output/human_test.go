package output

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/emailable/emailable-cli/internal/api"
)

func TestVerifyResult(t *testing.T) {
	var buf bytes.Buffer
	h := &Human{W: &buf}
	r := &api.VerifyResult{
		Email:    "x@y.com",
		State:    "deliverable",
		Score:    99,
		Reason:   "accepted_email",
		Domain:   "y.com",
		MXRecord: "mx.y.com",
	}
	if err := h.PrintVerifyResult(r); err != nil {
		t.Fatalf("print: %v", err)
	}
	got := buf.String()
	wantSubstrings := []string{
		"x@y.com",
		"99",
		"General",
		"State",
		"Deliverable",
		"Reason",
		"Accepted Email",
		"Domain",
		"y.com",
		"Attributes",
		"Free",
		"No",
		"Role",
		"Disposable",
		"Accept-All",
		"Mail Server",
		"MX Record",
		"mx.y.com",
	}
	for _, s := range wantSubstrings {
		if !strings.Contains(got, s) {
			t.Errorf("output missing %q\n--- output ---\n%s", s, got)
		}
	}
	// Lowercase API values should NOT appear in the rendered text.
	if strings.Contains(got, "deliverable") {
		t.Errorf("output should not contain lowercase 'deliverable':\n%s", got)
	}
	if strings.Contains(got, "accepted_email") {
		t.Errorf("output should not contain raw 'accepted_email':\n%s", got)
	}
}

func TestVerifyResult_AllFields(t *testing.T) {
	var buf bytes.Buffer
	h := &Human{W: &buf}
	r := &api.VerifyResult{
		Email:        "john.smith@gmail.com",
		State:        "deliverable",
		Score:        100,
		Reason:       "accepted_email",
		Duration:     0.493,
		Domain:       "gmail.com",
		SMTPProvider: "google",
		MXRecord:     "aspmx.l.google.com",
		DidYouMean:   "john.smith@gmail.co",
		Free:         true,
		MailboxFull:  false,
		NoReply:      false,
		User:         "john.smith",
		FirstName:    "John",
		LastName:     "Smith",
		FullName:     "John Smith",
		Gender:       "male",
		Tag:          "support",
	}
	if err := h.PrintVerifyResult(r); err != nil {
		t.Fatalf("print: %v", err)
	}
	got := buf.String()

	// All section labels and values should appear, in dashboard order:
	// header → General → Attributes → Mail Server. User and Duration are
	// intentionally not in the human view (--json still has them).
	wantOrdered := []string{
		"john.smith@gmail.com", "100", // header + score badge
		"General",
		"Full Name", "John Smith",
		"Gender", "Male",
		"State", "Deliverable",
		"Reason", "Accepted Email",
		"Domain", "gmail.com",
		"Did you mean", "john.smith@gmail.co",
		"Attributes",
		"Free", "Role", "Disposable", "Accept-All",
		"Tag", "support",
		"Mailbox Full", "No Reply",
		"Mail Server",
		"SMTP Provider", "google",
		"MX Record", "aspmx.l.google.com",
	}
	idx := 0
	for _, s := range wantOrdered {
		found := strings.Index(got[idx:], s)
		if found < 0 {
			t.Errorf("output missing %q at/after position %d\n--- output ---\n%s", s, idx, got)
			continue
		}
		idx += found + len(s)
	}

	// Sections should be separated by blank lines: header + 3 sections
	// (General / Attributes / Mail Server) means at least 3 blank-line
	// separators in the output.
	blankLines := strings.Count(got, "\n\n")
	if blankLines < 3 {
		t.Errorf("expected at least 3 blank-line separators, got %d:\n%s", blankLines, got)
	}

	// User and Duration are dropped from human view per dashboard parity.
	for _, missing := range []string{"User", "Duration"} {
		if strings.Contains(got, missing) {
			t.Errorf("human view should not contain %q (dashboard parity):\n%s", missing, got)
		}
	}
}

func TestHumanizeReason(t *testing.T) {
	cases := map[string]string{
		"accepted_email":     "Accepted Email",
		"low_deliverability": "Low Deliverability",
		"invalid_domain":     "Invalid Domain",
		"unexpected_error":   "Unexpected Error",
		"some_unknown_code":  "some_unknown_code",
		"":                   "",
	}
	for in, want := range cases {
		if got := humanizeReason(in); got != want {
			t.Errorf("humanizeReason(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBatchStatus(t *testing.T) {
	var buf bytes.Buffer
	h := &Human{W: &buf}
	s := &api.BatchStatus{ID: "abc123", Processed: 42, Total: 100}
	if err := h.PrintBatchStatus(s); err != nil {
		t.Fatalf("print: %v", err)
	}
	got := buf.String()
	// New compact layout: line 1 is "<glyph> Batch <id>", line 2 is the
	// indented "processed/total (pct%)" counter. Non-TTY buffer means
	// no color codes — assertions match plain text.
	for _, want := range []string{"Batch abc123", "42/100 (42%)"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output, got %q", want, got)
		}
	}
}

func TestBatchStatus_Complete(t *testing.T) {
	var buf bytes.Buffer
	h := &Human{W: &buf}
	s := &api.BatchStatus{ID: "abc123", Processed: 100, Total: 100}
	if err := h.PrintBatchStatus(s); err != nil {
		t.Fatalf("print: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"Batch abc123", "complete", "100/100"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output, got %q", want, got)
		}
	}
}

func TestBatchStatus_StartingUp(t *testing.T) {
	var buf bytes.Buffer
	h := &Human{W: &buf}
	s := &api.BatchStatus{Total: 0}
	if err := h.PrintBatchStatus(s); err != nil {
		t.Fatalf("print: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "(starting...)") {
		t.Errorf("expected starting marker, got %q", got)
	}
}

func TestBatchSummary(t *testing.T) {
	var buf bytes.Buffer
	h := &Human{W: &buf}
	status := &api.BatchStatus{
		// Total==Processed signals completion to IsComplete() so the
		// summary uses the "Verified" wording. Without this we'd land on
		// the partial-results branch instead.
		Total:     5,
		Processed: 5,
		Emails: []api.VerifyResult{
			{Email: "a@x.com", State: "deliverable"},
			{Email: "b@x.com", State: "deliverable"},
			{Email: "c@x.com", State: "deliverable"},
			{Email: "d@x.com", State: "undeliverable"},
			{Email: "e@x.com", State: "risky"},
		},
	}
	if err := h.PrintBatchSummary(status); err != nil {
		t.Fatalf("print: %v", err)
	}
	got := buf.String()
	for _, s := range []string{"Verified 5 emails", "3 Deliverable", "1 Undeliverable", "1 Risky"} {
		if !strings.Contains(got, s) {
			t.Errorf("output missing %q\n--- output ---\n%s", s, got)
		}
	}
	// Zero-count states should not appear.
	if strings.Contains(got, "0 unknown") {
		t.Errorf("unexpected zero-count state in output:\n%s", got)
	}
	if strings.Contains(got, "Partial") {
		t.Errorf("completed batch should not render Partial header:\n%s", got)
	}
}

// TestBatchSummary_Partial verifies the partial-results path: when the batch
// isn't complete (TotalCounts.Processed < Total) the header reads "Partial
// results (M of N processed)" instead of "Verified N emails".
func TestBatchSummary_Partial(t *testing.T) {
	var buf bytes.Buffer
	h := &Human{W: &buf}
	status := &api.BatchStatus{
		Message:     "batch is processing",
		TotalCounts: &api.BatchTotalCounts{Processed: 2, Total: 10},
		Emails: []api.VerifyResult{
			{Email: "a@x.com", State: "deliverable"},
			{Email: "b@x.com", State: "risky"},
		},
	}
	if err := h.PrintBatchSummary(status); err != nil {
		t.Fatalf("print: %v", err)
	}
	got := buf.String()
	for _, s := range []string{"Partial results", "2 of 10 processed", "1 Deliverable", "1 Risky"} {
		if !strings.Contains(got, s) {
			t.Errorf("output missing %q\n--- output ---\n%s", s, got)
		}
	}
	if strings.Contains(got, "Verified") {
		t.Errorf("partial batch should not render 'Verified' header:\n%s", got)
	}
}

// TestBatchSummary_EmptyEmails ensures rendering doesn't crash with an
// empty payload. An empty BatchStatus has no completion signals, so the
// partial-results branch fires.
func TestBatchSummary_EmptyEmails(t *testing.T) {
	var buf bytes.Buffer
	h := &Human{W: &buf}
	if err := h.PrintBatchSummary(&api.BatchStatus{}); err != nil {
		t.Fatalf("print: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "Partial results") {
		t.Errorf("expected partial header for empty batch, got:\n%s", got)
	}
}

func TestBatchResults_Table(t *testing.T) {
	var buf bytes.Buffer
	h := &Human{W: &buf}
	results := []api.VerifyResult{
		{Email: "a@x.com", State: "deliverable", Score: 100, Reason: "accepted_email"},
		{Email: "b@y.com", State: "undeliverable", Score: 0, Reason: "rejected_email"},
	}
	if err := h.PrintBatchResults(results); err != nil {
		t.Fatalf("print: %v", err)
	}
	got := buf.String()
	for _, s := range []string{"EMAIL", "STATE", "SCORE", "REASON", "a@x.com", "b@y.com", "Deliverable", "Undeliverable", "Accepted Email", "Rejected Email"} {
		if !strings.Contains(got, s) {
			t.Errorf("output missing %q\n--- output ---\n%s", s, got)
		}
	}
}

// TestBatchResults_MixedGlyphAlignment verifies that rows with different
// leading glyphs (multi-byte ✓/✗ vs single-byte ?) still line up at the
// same visual column. Previously len() was used to compute widths, which
// counted bytes rather than visual columns and threw off ASCII-glyph rows.
func TestBatchResults_MixedGlyphAlignment(t *testing.T) {
	var buf bytes.Buffer
	h := &Human{W: &buf}
	results := []api.VerifyResult{
		{Email: "a@example.com", State: "deliverable", Score: 100, Reason: "accepted_email"},
		{Email: "b@example.com", State: "risky", Score: 50, Reason: "low_deliverability"},
		{Email: "c@example.com", State: "undeliverable", Score: 0, Reason: "invalid_domain"},
	}
	if err := h.PrintBatchResults(results); err != nil {
		t.Fatalf("print: %v", err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	// Skip header + separator (the first two lines).
	if len(lines) < 5 {
		t.Fatalf("expected at least 5 lines, got %d:\n%s", len(lines), buf.String())
	}
	dataLines := lines[2:]

	// Find the visual column index at which the email starts on each row.
	// Strategy: each row begins with "<glyph><spaces>" then the email. The
	// email always starts with a letter; the glyph is one of ✓, ✗, ?, or " ".
	emailCols := make([]int, 0, len(dataLines))
	for _, line := range dataLines {
		// Locate the '@' to anchor on the email, then walk backwards to the
		// first character of the email (assumed not to be a space).
		at := strings.IndexByte(line, '@')
		if at < 0 {
			t.Fatalf("line missing '@': %q", line)
		}
		start := at
		for start > 0 && line[start-1] != ' ' {
			start--
		}
		// Visual column == lipgloss.Width of the prefix up to start.
		emailCols = append(emailCols, lipgloss.Width(line[:start]))
	}
	for i := 1; i < len(emailCols); i++ {
		if emailCols[i] != emailCols[0] {
			t.Errorf("email column misaligned: row 0 starts at col %d, row %d starts at col %d\n--- output ---\n%s",
				emailCols[0], i, emailCols[i], buf.String())
		}
	}
}

func TestAccount(t *testing.T) {
	var buf bytes.Buffer
	h := &Human{W: &buf}
	v := &AccountView{OwnerEmail: "x@y.com", AvailableCredits: 12345}
	if err := h.PrintAccountView(v); err != nil {
		t.Fatalf("print: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "Credits:  12,345") {
		t.Errorf("expected formatted credits, got %q", got)
	}
	if !strings.Contains(got, "Account:  x@y.com") {
		t.Errorf("expected account line, got %q", got)
	}
}

func TestPrint_Dispatch(t *testing.T) {
	cases := []struct {
		name string
		v    any
	}{
		{"VerifyResultPtr", &api.VerifyResult{Email: "x@y.com", State: "deliverable"}},
		{"VerifyResultVal", api.VerifyResult{Email: "x@y.com", State: "deliverable"}},
		{"BatchSubmitPtr", &api.BatchSubmit{ID: "abc123"}},
		{"BatchSubmitVal", api.BatchSubmit{ID: "abc123"}},
		{"BatchStatusProgress", &api.BatchStatus{Total: 10, Processed: 5}},
		{"BatchStatusEmails", &api.BatchStatus{Total: 1, Processed: 1, Emails: []api.VerifyResult{{Email: "a@x.com", State: "deliverable"}}}},
		{"BatchStatusDownload", &api.BatchStatus{Total: 5000, Processed: 5000, DownloadFile: "https://x/y"}},
		{"VerifyResultSlice", []api.VerifyResult{{Email: "a@x.com", State: "deliverable"}}},
		{"AccountViewPtr", &AccountView{OwnerEmail: "x@y.com", AvailableCredits: 100}},
		{"AccountViewVal", AccountView{OwnerEmail: "x@y.com", AvailableCredits: 100}},
		{"Fallback", map[string]any{"hello": "world"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			h := &Human{W: &buf}
			if err := h.Print(c.v); err != nil {
				t.Fatalf("print: %v", err)
			}
			if buf.Len() == 0 {
				t.Errorf("expected non-empty output")
			}
		})
	}
}

func TestFormatThousands(t *testing.T) {
	cases := map[int]string{
		0:        "0",
		5:        "5",
		1000:     "1,000",
		12345:    "12,345",
		1234567:  "1,234,567",
		-1234567: "-1,234,567",
	}
	for in, want := range cases {
		if got := formatThousands(in); got != want {
			t.Errorf("formatThousands(%d) = %q, want %q", in, got, want)
		}
	}
}

// TestHuman_QuietSuppressesChrome verifies that Quiet=true skips
// Success/Hint/Notice without writing anything to the underlying writer,
// while leaving the methods' nil-error contract intact.
func TestHuman_QuietSuppressesChrome(t *testing.T) {
	var buf bytes.Buffer
	h := &Human{W: &buf, Quiet: true}
	if err := h.Success("done"); err != nil {
		t.Fatalf("Success: %v", err)
	}
	if err := h.Hint("tip"); err != nil {
		t.Fatalf("Hint: %v", err)
	}
	if err := h.Notice("fyi"); err != nil {
		t.Fatalf("Notice: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output in quiet mode, got %q", buf.String())
	}
}

// TestHuman_QuietDefaultIsLoud verifies the zero value (Quiet=false) prints
// chrome as before — guards against an accidental flip of the default.
func TestHuman_QuietDefaultIsLoud(t *testing.T) {
	var buf bytes.Buffer
	h := &Human{W: &buf}
	if err := h.Success("done"); err != nil {
		t.Fatalf("Success: %v", err)
	}
	if !strings.Contains(buf.String(), "done") {
		t.Errorf("expected chrome line in non-quiet mode, got %q", buf.String())
	}
}
