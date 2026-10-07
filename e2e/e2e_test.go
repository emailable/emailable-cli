//go:build e2e

// Package e2e runs the built emailable binary against the real Emailable API
// using a test key. Test keys return simulated results and never spend
// credits, and the <state>@example.com and <flag>.<state>@example.com
// addresses make those results predictable.
//
// Run with: EMAILABLE_TEST_API_KEY=test_xxx make test-e2e
package e2e

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const keyEnv = "EMAILABLE_TEST_API_KEY"

// commandTimeout bounds every invocation so a command that never finishes
// fails the test instead of hanging CI.
const commandTimeout = 90 * time.Second

// largeBatchSize is the smallest batch the API answers with a download_file.
const largeBatchSize = 1001

var (
	binary  string
	apiKey  string
	workDir string
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	apiKey = os.Getenv(keyEnv)
	if apiKey == "" {
		fmt.Fprintf(os.Stderr, "skipping e2e tests: %s is not set\n", keyEnv)
		return 0
	}
	// A live key would spend real credits on every run.
	if !strings.HasPrefix(apiKey, "test_") {
		fmt.Fprintf(os.Stderr, "%s must be a test key (test_...)\n", keyEnv)
		return 1
	}

	tmp, err := os.MkdirTemp("", "emailable-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(tmp)

	binary = filepath.Join(tmp, "emailable")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = ".."
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "build: %v\n", err)
		return 1
	}

	// An empty working directory keeps a project-local .emailable/config.json
	// from leaking into the run.
	workDir = filepath.Join(tmp, "work")
	if err := os.Mkdir(workDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	return m.Run()
}

type result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// emailable runs the binary with the test key and returns its output and exit
// code. It fails the test only if the binary could not be run or timed out.
func emailable(t *testing.T, args ...string) result {
	t.Helper()
	return emailableWithKey(t, apiKey, args...)
}

func emailableWithKey(t *testing.T, key string, args ...string) result {
	t.Helper()
	res, err := execBinary(key, nil, args...)
	if err != nil {
		t.Fatalf("emailable %s: %v\nstderr: %s", strings.Join(args, " "), err, res.Stderr)
	}
	return res
}

func execBinary(key string, env []string, args ...string) (result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"EMAILABLE_API_KEY="+key,
		"EMAILABLE_NO_UPDATE_NOTIFIER=1",
		"EMAILABLE_OUTPUT=",
		"EMAILABLE_DEBUG=",
		"NO_COLOR=1",
	)
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	res := result{Stdout: stdout.String(), Stderr: stderr.String()}
	if ctx.Err() != nil {
		return res, fmt.Errorf("timed out after %s", commandTimeout)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	}
	return res, err
}

func decodeJSON(t *testing.T, s string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(s), v); err != nil {
		t.Fatalf("decode JSON: %v\noutput: %s", err, s)
	}
}

func requireExit(t *testing.T, res result, want int) {
	t.Helper()
	if res.ExitCode != want {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", res.ExitCode, want, res.Stdout, res.Stderr)
	}
}

type verifyResult struct {
	Email  string `json:"email"`
	State  string `json:"state"`
	Domain string `json:"domain"`
	Role   bool   `json:"role"`
}

type batchStatus struct {
	ID           string         `json:"id"`
	Message      string         `json:"message"`
	Emails       []verifyResult `json:"emails"`
	DownloadFile string         `json:"download_file"`
	TotalCounts  map[string]int `json:"total_counts"`
}

type expectation struct {
	email string
	state string
	role  bool
}

// A plain <state>@example.com address pins the state, and a
// <flag>.<state>@example.com address also pins that flag. Other fields are
// random, so only these are asserted.
var expectations = []expectation{
	{email: "deliverable@example.com", state: "deliverable"},
	{email: "undeliverable@example.com", state: "undeliverable"},
	{email: "role.undeliverable@example.com", state: "undeliverable", role: true},
}

func (e expectation) check(t *testing.T, got verifyResult) {
	t.Helper()
	if got.Email != e.email {
		t.Errorf("email = %q, want %q", got.Email, e.email)
	}
	if got.State != e.state {
		t.Errorf("%s: state = %q, want %q", e.email, got.State, e.state)
	}
	if e.role && !got.Role {
		t.Errorf("%s: role = false, want true", e.email)
	}
}

func TestAccountStatus(t *testing.T) {
	res := emailable(t, "account", "status", "--json")
	requireExit(t, res, 0)

	var account struct {
		OwnerEmail       string `json:"owner_email"`
		AvailableCredits *int   `json:"available_credits"`
	}
	decodeJSON(t, res.Stdout, &account)
	if !strings.Contains(account.OwnerEmail, "@") {
		t.Errorf("owner_email = %q, want an email address", account.OwnerEmail)
	}
	if account.AvailableCredits == nil {
		t.Error("available_credits is missing")
	}
}

func TestAccountStatus_InvalidKey(t *testing.T) {
	res := emailableWithKey(t, "test_invalid", "account", "status", "--json")
	requireExit(t, res, 2)

	var e struct {
		Code string `json:"code"`
	}
	decodeJSON(t, res.Stderr, &e)
	// The API answers an unknown key with 403, not 401.
	if e.Code != "forbidden" {
		t.Errorf("code = %q, want forbidden", e.Code)
	}
}

func TestVerify(t *testing.T) {
	for _, e := range expectations {
		t.Run(e.email, func(t *testing.T) {
			res := emailable(t, "verify", e.email, "--json")
			requireExit(t, res, 0)

			var got verifyResult
			decodeJSON(t, res.Stdout, &got)
			e.check(t, got)
			if got.Domain != "example.com" {
				t.Errorf("domain = %q, want example.com", got.Domain)
			}
		})
	}
}

func TestVerify_Human(t *testing.T) {
	res := emailable(t, "verify", "deliverable@example.com")
	requireExit(t, res, 0)

	if !strings.Contains(res.Stdout, "deliverable@example.com") {
		t.Errorf("output missing email:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "Deliverable") {
		t.Errorf("output missing humanized state:\n%s", res.Stdout)
	}
}

// slow@example.com always returns 249, so the CLI retries and then gives up
// with try_again.
func TestVerify_Slow(t *testing.T) {
	res := emailable(t, "verify", "slow@example.com", "--json")
	requireExit(t, res, 3)

	var e struct {
		Code string `json:"code"`
	}
	decodeJSON(t, res.Stderr, &e)
	if e.Code != "try_again" {
		t.Errorf("code = %q, want try_again", e.Code)
	}
}

func emailList(es []expectation) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.email
	}
	return out
}

func checkBatchEmails(t *testing.T, got batchStatus) {
	t.Helper()
	if len(got.Emails) != len(expectations) {
		t.Fatalf("got %d emails, want %d\n%+v", len(got.Emails), len(expectations), got)
	}
	byEmail := make(map[string]verifyResult, len(got.Emails))
	for _, r := range got.Emails {
		byEmail[r.Email] = r
	}
	states := map[string]int{}
	for _, e := range expectations {
		r, ok := byEmail[e.email]
		if !ok {
			t.Errorf("missing result for %s", e.email)
			continue
		}
		e.check(t, r)
		states[e.state]++
	}
	if got.TotalCounts["total"] != len(expectations) {
		t.Errorf("total_counts.total = %d, want %d", got.TotalCounts["total"], len(expectations))
	}
	for state, n := range states {
		if got.TotalCounts[state] != n {
			t.Errorf("total_counts.%s = %d, want %d", state, got.TotalCounts[state], n)
		}
	}
}

func submitBatch(t *testing.T, args ...string) string {
	t.Helper()
	res := emailable(t, append([]string{"batch", "verify", "--json"}, args...)...)
	requireExit(t, res, 0)

	var submit struct {
		ID string `json:"id"`
	}
	decodeJSON(t, res.Stdout, &submit)
	if submit.ID == "" {
		t.Fatalf("batch verify returned no id: %s", res.Stdout)
	}
	return submit.ID
}

func TestBatch_SubmitAndGet(t *testing.T) {
	id := submitBatch(t, emailList(expectations)...)

	res := emailable(t, "batch", "get", id, "--json")
	requireExit(t, res, 0)

	var got batchStatus
	decodeJSON(t, res.Stdout, &got)
	if got.ID != id {
		t.Errorf("id = %q, want %q", got.ID, id)
	}
	checkBatchEmails(t, got)
}

func TestBatch_FromCSV(t *testing.T) {
	path := filepath.Join(t.TempDir(), "emails.csv")
	body := "email\n" + strings.Join(emailList(expectations), "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	id := submitBatch(t, path)

	res := emailable(t, "batch", "get", id, "--json")
	requireExit(t, res, 0)

	var got batchStatus
	decodeJSON(t, res.Stdout, &got)
	checkBatchEmails(t, got)
}

func TestBatch_VerifyWait(t *testing.T) {
	res := emailable(t, append([]string{"batch", "verify", "--wait", "--json"}, emailList(expectations)...)...)
	requireExit(t, res, 0)

	var got batchStatus
	decodeJSON(t, res.Stdout, &got)
	checkBatchEmails(t, got)
}

func TestBatch_GetOutputCSV(t *testing.T) {
	id := submitBatch(t, emailList(expectations)...)
	path := filepath.Join(t.TempDir(), "results.csv")

	res := emailable(t, "batch", "get", id, "-o", path)
	requireExit(t, res, 0)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != len(expectations)+1 {
		t.Fatalf("got %d CSV lines, want %d (header plus one per email)\n%s", len(lines), len(expectations)+1, data)
	}
	if !strings.HasPrefix(lines[0], "email,state,") {
		t.Errorf("unexpected CSV header %q", lines[0])
	}
}

// Batches over 1,000 emails return a download_file instead of inline emails.
func TestBatch_Large(t *testing.T) {
	path := filepath.Join(t.TempDir(), "emails.txt")
	var b strings.Builder
	for i := 0; i < largeBatchSize; i++ {
		fmt.Fprintf(&b, "deliverable+%d@example.com\n", i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	id := submitBatch(t, path)

	res := emailable(t, "batch", "get", id, "--json")
	requireExit(t, res, 0)

	var got batchStatus
	decodeJSON(t, res.Stdout, &got)
	if !strings.HasPrefix(got.DownloadFile, "https://") {
		t.Errorf("download_file = %q, want an https URL", got.DownloadFile)
	}
	if len(got.Emails) != 0 {
		t.Errorf("got %d inline emails, want none for a large batch", len(got.Emails))
	}
	if got.TotalCounts["total"] != largeBatchSize {
		t.Errorf("total_counts.total = %d, want %d", got.TotalCounts["total"], largeBatchSize)
	}
	if got.DownloadFile != "" {
		if n := downloadedRows(t, got.DownloadFile); n != largeBatchSize {
			t.Errorf("download_file has %d rows, want %d", n, largeBatchSize)
		}
	}
}

// downloadedRows fetches a large batch's download_file, a zipped CSV, and
// returns its row count, excluding the header.
func downloadedRows(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("unzip: %v", err)
	}
	if len(zr.File) != 1 {
		t.Fatalf("zip has %d files, want 1", len(zr.File))
	}
	f, err := zr.File[0].Open()
	if err != nil {
		t.Fatalf("unzip: %v", err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("read CSV: %v", err)
	}
	return len(rows) - 1
}

func TestBatch_GetUnknownID(t *testing.T) {
	res := emailable(t, "batch", "get", "000000000000000000000000", "--json")
	requireExit(t, res, 4)

	var e struct {
		Code string `json:"code"`
	}
	decodeJSON(t, res.Stderr, &e)
	if e.Code != "not_found" {
		t.Errorf("code = %q, want not_found", e.Code)
	}
}

func TestBatch_FromJSONWithField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "contacts.json")
	var items []map[string]string
	for _, e := range expectations {
		items = append(items, map[string]string{"address": e.email})
	}
	body, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	id := submitBatch(t, path, "--field", "address")

	res := emailable(t, "batch", "get", id, "--json")
	requireExit(t, res, 0)

	var got batchStatus
	decodeJSON(t, res.Stdout, &got)
	checkBatchEmails(t, got)
}

func TestBatch_ResponseFields(t *testing.T) {
	id := submitBatch(t, append([]string{"--response-fields", "email,state"}, emailList(expectations)...)...)

	res := emailable(t, "batch", "get", id, "--json")
	requireExit(t, res, 0)

	var got struct {
		Emails []map[string]any `json:"emails"`
	}
	decodeJSON(t, res.Stdout, &got)
	if len(got.Emails) != len(expectations) {
		t.Fatalf("got %d emails, want %d", len(got.Emails), len(expectations))
	}
	for _, e := range got.Emails {
		if len(e) != 2 || e["email"] == nil || e["state"] == nil {
			t.Errorf("got fields %v, want only email and state", e)
		}
	}
}

// The API accepts the callback URL and retries flag without echoing them, so
// this only checks that the CLI sends them in a form the API accepts.
func TestBatch_URLAndRetries(t *testing.T) {
	submitBatch(t, append([]string{"--url", "https://example.com/callback", "--retries=false"}, emailList(expectations)...)...)
}

func TestBatch_GetPartial(t *testing.T) {
	id := submitBatch(t, emailList(expectations)...)

	res := emailable(t, "batch", "get", id, "--partial", "--json")
	requireExit(t, res, 0)

	var got batchStatus
	decodeJSON(t, res.Stdout, &got)
	checkBatchEmails(t, got)
}

func TestBatch_GetHuman(t *testing.T) {
	id := submitBatch(t, emailList(expectations)...)

	res := emailable(t, "batch", "get", id)
	requireExit(t, res, 0)
	for _, want := range []string{"1 Deliverable", "2 Undeliverable"} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("summary missing %q:\n%s", want, res.Stdout)
		}
	}

	res = emailable(t, "batch", "get", id, "--all")
	requireExit(t, res, 0)
	for _, e := range expectations {
		if !strings.Contains(res.Stdout, e.email) {
			t.Errorf("--all output missing %s:\n%s", e.email, res.Stdout)
		}
	}
}

func TestBatch_VerifyWaitOutputJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.json")
	res := emailable(t, append([]string{"batch", "verify", "--wait", "-o", path}, emailList(expectations)...)...)
	requireExit(t, res, 0)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got batchStatus
	decodeJSON(t, string(data), &got)
	checkBatchEmails(t, got)
}

// rateLimitBurst is more concurrent requests than the batch status limit
// allows in one second, so some of them get a 429. The default limit is 5,
// and the test account's is 25.
const rateLimitBurst = 40

// TestRateLimit sends a burst of batch status requests and checks that the
// CLI retries every 429 it gets until the request succeeds.
func TestRateLimit(t *testing.T) {
	id := submitBatch(t, emailList(expectations)...)

	results := make([]result, rateLimitBurst)
	errs := make([]error, rateLimitBurst)
	done := make(chan struct{})
	for i := range rateLimitBurst {
		go func() {
			results[i], errs[i] = execBinary(apiKey, []string{"EMAILABLE_DEBUG=1"}, "batch", "get", id, "--json")
			done <- struct{}{}
		}()
	}
	for range rateLimitBurst {
		<-done
	}

	limited := 0
	for i, res := range results {
		if errs[i] != nil {
			t.Fatalf("request %d: %v", i, errs[i])
		}
		if res.ExitCode != 0 {
			t.Errorf("request %d: exit code = %d, want 0\nstderr: %s", i, res.ExitCode, res.Stderr)
		}
		if strings.Contains(res.Stderr, " 429 Too Many Requests") {
			limited++
		}
	}
	if limited == 0 {
		t.Skipf("no 429 in %d concurrent requests; the account's limit may be higher", rateLimitBurst)
	}
	t.Logf("%d of %d requests were rate limited and retried", limited, rateLimitBurst)
}
