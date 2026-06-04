package cmd

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/emailable/emailable-cli/internal/api"
	"github.com/spf13/cobra"
)

// Aliases for os file functions used by the small helper wrappers below.
var (
	osWriteFile = os.WriteFile
	osReadFile  = os.ReadFile
)

// completedBatchPayload returns the canonical "completed small batch" shape
// the Emailable API would return for two emails.
func completedBatchPayload(id string) map[string]any {
	return map[string]any{
		"id": id,
		"emails": []map[string]any{
			{"email": "a@example.com", "state": "deliverable", "score": 100, "reason": "accepted_email"},
			{"email": "b@example.com", "state": "undeliverable", "score": 0, "reason": "invalid_email"},
		},
		"reason_counts": map[string]int{"accepted_email": 1, "invalid_email": 1},
	}
}

// TestBatchVerify_Submit_HappyPath_JSON covers POST /v1/batch with --json (no
// polling): the submit response passes through unchanged, so both id and
// message reach stdout rather than a reshaped {"id":...}.
func TestBatchVerify_Submit_HappyPath_JSON(t *testing.T) {
	var capturedForm string
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/batch" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r.Body)
		capturedForm = buf.String()
		writeJSON(w, map[string]any{
			"id":      "bch_abc123",
			"message": "Batch submitted",
		})
	}))
	env.seedAPIKey(t, "sk_test_xxx")

	res := runRoot(t, "batch", "verify", "a@x.com", "b@y.com", "--json")
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	payload := decodeJSON(t, res.Stdout.Bytes())
	if payload["id"] != "bch_abc123" {
		t.Errorf("expected id in JSON, got %v", payload)
	}
	if payload["message"] != "Batch submitted" {
		t.Errorf("expected submit message to pass through, got %v", payload["message"])
	}
	if !strings.Contains(capturedForm, "emails=a%40x.com%2Cb%40y.com") {
		t.Errorf("expected comma-joined emails in form body, got %q", capturedForm)
	}
}

// TestBatchVerify_FromCSV checks file input flows through collectEmails to
// the submit form body.
func TestBatchVerify_FromCSV(t *testing.T) {
	var capturedForm string
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r.Body)
		capturedForm = buf.String()
		writeJSON(w, map[string]any{"id": "bch_csv"})
	}))
	env.seedAPIKey(t, "sk_test_xxx")

	csvPath := filepath.Join(t.TempDir(), "in.csv")
	if err := writeFile(csvPath, "email\na@x.com\nb@y.com\n"); err != nil {
		t.Fatalf("write csv: %v", err)
	}

	res := runRoot(t, "batch", "verify", csvPath, "--json")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	if !strings.Contains(capturedForm, "emails=a%40x.com%2Cb%40y.com") {
		t.Errorf("expected csv emails in form, got %q", capturedForm)
	}
}

// TestBatchVerify_FromStdin verifies `-` reads emails from stdin.
func TestBatchVerify_FromStdin(t *testing.T) {
	withStdinSource(t, "a@x.com\nb@y.com\n", true)
	var capturedForm string
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r.Body)
		capturedForm = buf.String()
		writeJSON(w, map[string]any{"id": "bch_stdin"})
	}))
	env.seedAPIKey(t, "sk_test_xxx")

	res := runRoot(t, "batch", "verify", "-", "--json")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	if !strings.Contains(capturedForm, "a%40x.com") || !strings.Contains(capturedForm, "b%40y.com") {
		t.Errorf("expected stdin emails in form, got %q", capturedForm)
	}
}

// TestBatchVerify_FlagsForwarded validates --url, --retries, and
// --response-fields are threaded into the submit body.
func TestBatchVerify_FlagsForwarded(t *testing.T) {
	var capturedForm string
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r.Body)
		capturedForm = buf.String()
		writeJSON(w, map[string]any{"id": "bch_flags"})
	}))
	env.seedAPIKey(t, "sk_test_xxx")

	res := runRoot(t, "batch", "verify", "a@x.com",
		"--url", "https://hook.example/r",
		"--retries=false",
		"--response-fields", "email,state",
		"--json",
	)
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	for _, want := range []string{"url=https", "retries=false", "response_fields=email%2Cstate"} {
		if !strings.Contains(capturedForm, want) {
			t.Errorf("expected %q in form body, got %q", want, capturedForm)
		}
	}
}

// TestBatchGet_Complete validates the happy-path GET /v1/batch flow rendering
// per-email summary output.
func TestBatchGet_Complete(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/batch" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("id"); got != "bch_abc" {
			t.Errorf("expected id=bch_abc, got %q", got)
		}
		writeJSON(w, completedBatchPayload("bch_abc"))
	}))
	env.seedAPIKey(t, "sk_test_xxx")

	res := runRoot(t, "batch", "get", "bch_abc")
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	out := res.Stdout.String()
	if !strings.Contains(out, "Verified 2 emails") {
		t.Errorf("expected summary line in output, got %q", out)
	}
}

// TestBatchGet_JSON exercises the full --json passthrough.
func TestBatchGet_JSON(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, completedBatchPayload("bch_json"))
	}))
	env.seedAPIKey(t, "sk_test_xxx")

	res := runRoot(t, "batch", "get", "bch_json", "--json")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	payload := decodeJSON(t, res.Stdout.Bytes())
	if payload["id"] != "bch_json" {
		t.Errorf("expected id, got %v", payload)
	}
	emails, ok := payload["emails"].([]any)
	if !ok || len(emails) != 2 {
		t.Errorf("expected 2 emails in payload, got %v", payload["emails"])
	}
}

// rawBatchPayload is a completed batch whose body carries the full
// total_counts state breakdown, per-row null fields, and a field the typed
// struct doesn't model — the cases a struct round-trip would lose.
const rawBatchPayload = `{"id":"bch_raw","emails":[{"email":"a@b.com","state":"risky","accept_all":null,"tag":null}],"reason_counts":{"accepted_email":1},"total_counts":{"deliverable":1,"undeliverable":2,"risky":3,"unknown":4,"duplicate":5,"processed":15,"total":15},"future_field":"keep-me"}`

// TestBatchGet_JSON_PassesThroughVerbatim pins passthrough for `batch get
// --json`: the full total_counts breakdown (the typed struct keeps only
// total/processed), per-row nulls, and unmodeled fields must all survive.
func TestBatchGet_JSON_PassesThroughVerbatim(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(rawBatchPayload))
	}))
	env.seedAPIKey(t, "sk_test_xxx")

	res := runRoot(t, "batch", "get", "bch_raw", "--json")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	out := res.Stdout.String()
	for _, want := range []string{
		`"deliverable": 1`, `"undeliverable": 2`, `"risky": 3`,
		`"unknown": 4`, `"duplicate": 5`,
		`"accept_all": null`, `"tag": null`,
		`"future_field": "keep-me"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q to pass through unchanged, got:\n%s", want, out)
		}
	}
}

// TestBatchGet_SaveJSON_PassesThroughVerbatim confirms the same fidelity for a
// saved .json file (the -o path).
func TestBatchGet_SaveJSON_PassesThroughVerbatim(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(rawBatchPayload))
	}))
	env.seedAPIKey(t, "sk_test_xxx")

	out := filepath.Join(t.TempDir(), "results.json")
	res := runRoot(t, "batch", "get", "bch_raw", "-o", out)
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	data, err := readFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	for _, want := range []string{`"duplicate": 5`, `"accept_all": null`, `"future_field": "keep-me"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("expected %q in saved file, got:\n%s", want, data)
		}
	}
}

// TestBatchGet_Partial: --partial passes partial=true on the query.
func TestBatchGet_Partial(t *testing.T) {
	var sawPartial bool
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("partial") == "true" {
			sawPartial = true
		}
		writeJSON(w, completedBatchPayload("bch_p"))
	}))
	env.seedAPIKey(t, "sk_test_xxx")

	res := runRoot(t, "batch", "get", "bch_p", "--partial", "--json")
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	if !sawPartial {
		t.Error("expected partial=true on query")
	}
}

// TestBatchGet_WaitAndPartialConflict surfaces the local validation error
// without contacting the server.
func TestBatchGet_WaitAndPartialConflict(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server should not be hit")
	}))
	env.seedAPIKey(t, "sk_test_xxx")

	res := runRoot(t, "batch", "get", "bch_x", "--wait", "--partial")
	if res.Err == nil {
		t.Fatal("expected conflict error")
	}
	if !strings.Contains(res.Err.Error(), "--wait and --partial") {
		t.Errorf("unexpected error: %v", res.Err)
	}
}

// TestBatchGet_Wait polls verifying twice then returns complete. The fast
// poll interval (1s) means this test takes ~2s; acceptable for an integration
// test and the polling logic is otherwise uncoverable.
func TestBatchGet_Wait(t *testing.T) {
	if testing.Short() {
		t.Skip("polls multiple times, slow")
	}
	var calls int32
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		// Return "verifying" the first 2 times, then "complete".
		if n < 3 {
			writeJSON(w, map[string]any{
				"id":        "bch_w",
				"total":     2,
				"processed": int(n) - 1,
				"status":    "verifying",
			})
			return
		}
		writeJSON(w, completedBatchPayload("bch_w"))
	}))
	env.seedAPIKey(t, "sk_test_xxx")

	res := runRoot(t, "batch", "get", "bch_w", "--wait", "--json")
	if res.Err != nil {
		t.Fatalf("execute: %v\nstderr: %s", res.Err, res.Stderr.String())
	}
	if atomic.LoadInt32(&calls) < 3 {
		t.Errorf("expected at least 3 polls, got %d", calls)
	}
	payload := decodeJSON(t, res.Stdout.Bytes())
	if emails, ok := payload["emails"].([]any); !ok || len(emails) != 2 {
		t.Errorf("expected final completed payload, got %v", payload)
	}
}

func TestSubmitBatchOptionsFromFlags_NoneSet(t *testing.T) {
	verify := &cobra.Command{Use: "verify"}
	verify.Flags().String("url", "", "")
	verify.Flags().Bool("retries", true, "")
	verify.Flags().StringSlice("response-fields", nil, "")
	opts, err := submitBatchOptionsFromFlags(verify)
	if err != nil {
		t.Fatal(err)
	}
	if opts != nil {
		t.Errorf("expected nil when no flags changed, got %+v", opts)
	}
}

func TestSubmitBatchOptionsFromFlags_AllSet(t *testing.T) {
	verify := &cobra.Command{Use: "verify"}
	verify.Flags().String("url", "", "")
	verify.Flags().Bool("retries", true, "")
	verify.Flags().StringSlice("response-fields", nil, "")
	if err := verify.Flags().Set("url", "https://h.example"); err != nil {
		t.Fatal(err)
	}
	if err := verify.Flags().Set("retries", "false"); err != nil {
		t.Fatal(err)
	}
	if err := verify.Flags().Set("response-fields", "email,state"); err != nil {
		t.Fatal(err)
	}
	opts, err := submitBatchOptionsFromFlags(verify)
	if err != nil {
		t.Fatal(err)
	}
	if opts == nil {
		t.Fatal("expected non-nil opts")
	}
	if opts.URL != "https://h.example" {
		t.Errorf("URL: got %q", opts.URL)
	}
	if opts.Retries == nil || *opts.Retries != false {
		t.Errorf("Retries: got %v", opts.Retries)
	}
	if got := strings.Join(opts.ResponseFields, ","); got != "email,state" {
		t.Errorf("ResponseFields: got %q", got)
	}
}

func TestSavedMessage(t *testing.T) {
	cases := []struct {
		n    int
		path string
		want string
	}{
		{0, "/tmp/x", "Saved to /tmp/x"},
		{1, "/tmp/x", "Saved 1 result to /tmp/x"},
		{5, "/tmp/x", "Saved 5 results to /tmp/x"},
	}
	for _, tc := range cases {
		if got := savedMessage(tc.n, tc.path); got != tc.want {
			t.Errorf("savedMessage(%d,%q): got %q want %q", tc.n, tc.path, got, tc.want)
		}
	}
}

// TestPrintBatchID confirms the helper writes a key/value line containing
// the id. The dim style is suppressed for non-TTY writers, so the raw ID
// shows up verbatim in the captured buffer.
func TestPrintBatchID(t *testing.T) {
	var buf bytes.Buffer
	printBatchID(&buf, "bch_abc")
	if !strings.Contains(buf.String(), "bch_abc") {
		t.Errorf("expected id in output, got %q", buf.String())
	}
}

// TestRenderBatchOutcome_OutputFile checks the --output path delegates to
// saveToFile and writes the configured file.
func TestRenderBatchOutcome_OutputFile(t *testing.T) {
	resetJSONFlag(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "results.json")

	cmd := &cobra.Command{}
	var stderr bytes.Buffer
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&stderr)

	status := &api.BatchStatus{
		ID: "bch_out",
		Emails: []api.VerifyResult{
			{Email: "a@b.com", State: "deliverable", Score: 100},
		},
	}
	cctx := &cmdCtx{JSONMode: false}
	if err := renderBatchOutcome(cmd, cctx, status, "bch_out", out, false); err != nil {
		t.Fatalf("renderBatchOutcome: %v", err)
	}
	// File must exist.
	data, err := readFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !strings.Contains(string(data), "a@b.com") {
		t.Errorf("expected email in output file, got %q", data)
	}
}

// TestRenderBatchOutcome_JSON dumps the full status struct to stdout.
func TestRenderBatchOutcome_JSON(t *testing.T) {
	prev := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = prev })

	var stdout bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&stdout)

	status := &api.BatchStatus{ID: "bch_j", Emails: []api.VerifyResult{{Email: "a@b.com"}}}
	cctx := &cmdCtx{JSONMode: true}
	if err := renderBatchOutcome(cmd, cctx, status, "bch_j", "", false); err != nil {
		t.Fatalf("renderBatchOutcome: %v", err)
	}
	if !strings.Contains(stdout.String(), `"id"`) {
		t.Errorf("expected JSON id field, got %q", stdout.String())
	}
}

// TestRenderBatchOutcome_DownloadFile covers the large-batch hint path.
func TestRenderBatchOutcome_DownloadFile(t *testing.T) {
	resetJSONFlag(t)
	var stdout bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})

	status := &api.BatchStatus{ID: "bch_big", DownloadFile: "https://files.example/big.csv"}
	cctx := &cmdCtx{JSONMode: false}
	if err := renderBatchOutcome(cmd, cctx, status, "bch_big", "", false); err != nil {
		t.Fatalf("renderBatchOutcome: %v", err)
	}
	if !strings.Contains(stdout.String(), "big.csv") {
		t.Errorf("expected download URL in output, got %q", stdout.String())
	}
}

// writeFile is a thin wrapper around os.WriteFile used by tests to drop
// fixture files into a tempdir.
func writeFile(p, body string) error { return osWriteFile(p, []byte(body), 0o644) }

// readFile reads an entire file. Trivial wrapper for symmetry with writeFile.
func readFile(p string) ([]byte, error) { return osReadFile(p) }
