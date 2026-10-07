//go:build e2e

// Package e2e runs the built emailable binary against the real Emailable API
// using a test key. Test keys return simulated results and never spend
// credits, and the <state>@example.com and <flag>.<state>@example.com
// addresses make those results predictable.
//
// Run with: EMAILABLE_TEST_API_KEY=test_xxx make test-e2e
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	before, err := availableCredits()
	if err != nil {
		fmt.Fprintf(os.Stderr, "account status: %v\n", err)
		return 1
	}

	code := m.Run()

	// Test keys must never spend credits. The account may be in use
	// elsewhere, so only a drop the size of the large batch counts as ours.
	after, err := availableCredits()
	if err != nil {
		fmt.Fprintf(os.Stderr, "account status: %v\n", err)
		return 1
	}
	if before-after >= largeBatchSize {
		fmt.Fprintf(os.Stderr, "available_credits dropped from %d to %d during the run\n", before, after)
		return 1
	}
	return code
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
	res, err := execBinary(key, args...)
	if err != nil {
		t.Fatalf("emailable %s: %v\nstderr: %s", strings.Join(args, " "), err, res.Stderr)
	}
	return res
}

func execBinary(key string, args ...string) (result, error) {
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

func availableCredits() (int, error) {
	res, err := execBinary(apiKey, "account", "status", "--json")
	if err != nil {
		return 0, err
	}
	if res.ExitCode != 0 {
		return 0, fmt.Errorf("exit %d: %s", res.ExitCode, res.Stderr)
	}
	var account struct {
		AvailableCredits *int `json:"available_credits"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &account); err != nil {
		return 0, fmt.Errorf("decode %q: %w", res.Stdout, err)
	}
	if account.AvailableCredits == nil {
		return 0, fmt.Errorf("available_credits missing from %q", res.Stdout)
	}
	return *account.AvailableCredits, nil
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
