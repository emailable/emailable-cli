package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emailable/emailable-cli/internal/config"
)

// testEnv configures a hermetic per-test environment:
//
//   - Spins up an httptest.Server with the supplied handler. The server's URL
//     is exported via EMAILABLE_API_URL/EMAILABLE_OAUTH_URL so cmd code paths
//     that resolve env.Current() pick it up.
//   - Points XDG_CONFIG_HOME at a temp dir so config.DefaultPath writes inside
//     the test sandbox.
//   - chdir's into a fresh empty temp dir so any project-local .emailable.yml
//     in the repo doesn't bleed into env.Current().
//
// The server, the config path, and the temp config dir are returned. Cleanup
// (server shutdown, env restoration, chdir restoration) is registered on t.
type testEnv struct {
	Server     *httptest.Server
	ConfigPath string
}

// newTestEnv builds a testEnv. handler is the response policy for the test
// server; pass http.NotFoundHandler() when the command shouldn't make calls.
func newTestEnv(t *testing.T, handler http.Handler) *testEnv {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	t.Chdir(t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// Point both the API and OAuth URLs at the test server. env.Current()
	// requires both or neither — partial overrides are a configuration
	// error, not the test's intent.
	t.Setenv("EMAILABLE_API_URL", srv.URL)
	t.Setenv("EMAILABLE_OAUTH_URL", srv.URL)
	// Force a clean credential resolution: EMAILABLE_API_KEY env var would
	// otherwise short-circuit OAuth code paths.
	t.Setenv("EMAILABLE_API_KEY", "")
	t.Setenv("EMAILABLE_DEBUG", "")
	t.Setenv("EMAILABLE_OUTPUT", "")

	// Reset persistent flag globals between tests since they're mutated in
	// place by cobra's flag binding.
	resetJSONFlag(t)
	prevDebug := debugMode
	debugMode = false
	t.Cleanup(func() { debugMode = prevDebug })
	prevAPIKey := apiKey
	apiKey = ""
	t.Cleanup(func() { apiKey = prevAPIKey })
	prevQuiet := quietMode
	quietMode = false
	t.Cleanup(func() { quietMode = prevQuiet })
	prevNoColor := noColor
	noColor = false
	t.Cleanup(func() { noColor = prevNoColor })

	// env.Current() returns "custom" when EMAILABLE_API_URL is set.
	path, err := config.DefaultPath("custom")
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}

	return &testEnv{Server: srv, ConfigPath: path}
}

// seedAPIKey writes an API key into the active test config so cmds appear
// "logged in" without going through the OAuth path.
func (e *testEnv) seedAPIKey(t *testing.T, key string) {
	t.Helper()
	cfg := &config.Config{APIKey: key}
	if err := cfg.Save(e.ConfigPath); err != nil {
		t.Fatalf("seed config: %v", err)
	}
}

// runCmd executes the root command with args and returns the captured
// stdout, stderr, and the RunE error (if any). stdout/stderr are routed
// through two separate buffers so tests can assert independently — cobra's
// default would send stderr to os.Stderr.
type runResult struct {
	Stdout *bytes.Buffer
	Stderr *bytes.Buffer
	Err    error
}

// runRoot builds a fresh root command, wires the buffers, sets args, and
// executes. Returns the captured output and the cobra error. Each call uses
// a fresh root so persistent flag state from earlier tests doesn't leak.
func runRoot(t *testing.T, args ...string) *runResult {
	t.Helper()
	root := newRootCmd("test")
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	err := root.Execute()
	return &runResult{Stdout: &stdout, Stderr: &stderr, Err: err}
}

// decodeJSON parses out as JSON into a generic map. Fails the test if the
// bytes don't parse — the tests below only ever invoke this on output we
// generated with --json.
func decodeJSON(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode JSON %q: %v", data, err)
	}
	return m
}

// jsonError encodes (statusCode, errorBody) as a stub-handler convenience.
// The handler writes a JSON body shaped like the Emailable API's documented
// error envelope: {"message": "...", "code": "..."}.
func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"message": message,
		"code":    code,
	})
}

// writeJSON writes v as the response body with a 200 status code.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
