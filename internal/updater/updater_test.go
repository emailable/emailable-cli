package updater

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// withServer spins up an httptest server, points the package-level
// ReleasesAPIURL at it for the duration of the test, and restores the
// original URL on cleanup. This is the only way tests should reach a
// "GitHub" — never the real network.
func withServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	orig := ReleasesAPIURL
	ReleasesAPIURL = srv.URL
	t.Cleanup(func() {
		ReleasesAPIURL = orig
		srv.Close()
	})
	return srv
}

// jsonHandler is a convenience that returns the given JSON body with 200 OK.
func jsonHandler(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
}

func TestCheck_HappyPath_UpdateAvailable(t *testing.T) {
	withServer(t, jsonHandler(`{"tag_name":"v0.2.0"}`))
	dir := t.TempDir()

	r := Check(context.Background(), "0.1.0", dir)
	if !r.UpdateAvailable {
		t.Fatalf("expected UpdateAvailable=true, got %+v", r)
	}
	if r.CurrentVersion != "0.1.0" {
		t.Errorf("CurrentVersion = %q, want 0.1.0", r.CurrentVersion)
	}
	if r.LatestVersion != "0.2.0" {
		t.Errorf("LatestVersion = %q, want 0.2.0", r.LatestVersion)
	}
	notice := FormatNotice(r, false)
	if !strings.Contains(notice, "0.1.0 → 0.2.0") {
		t.Errorf("notice missing version arrow: %q", notice)
	}
	if !strings.Contains(notice, ReleasesPageURL) {
		t.Errorf("notice missing releases URL: %q", notice)
	}
}

func TestCheck_NoUpdate_SameVersion(t *testing.T) {
	withServer(t, jsonHandler(`{"tag_name":"v0.1.0"}`))
	r := Check(context.Background(), "0.1.0", t.TempDir())
	if r.UpdateAvailable {
		t.Fatalf("expected UpdateAvailable=false, got %+v", r)
	}
	if got := FormatNotice(r, false); got != "" {
		t.Errorf("expected no notice when versions equal, got %q", got)
	}
}

func TestCheck_NoUpdate_CurrentIsAhead(t *testing.T) {
	withServer(t, jsonHandler(`{"tag_name":"v0.1.0"}`))
	r := Check(context.Background(), "1.0.0", t.TempDir())
	if r.UpdateAvailable {
		t.Errorf("local newer than remote should not flag update: %+v", r)
	}
}

func TestCheck_CacheHit_SkipsServer(t *testing.T) {
	// Server fails the test if hit — proves cache is used.
	withServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("server was hit even though a fresh cache exists")
	}))

	dir := t.TempDir()
	seed := cacheEntry{CheckedAt: time.Now().UTC().Add(-time.Hour), LatestVersion: "0.5.0"}
	b, _ := json.Marshal(seed)
	if err := os.WriteFile(filepath.Join(dir, "update-check.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}

	r := Check(context.Background(), "0.1.0", dir)
	if r.LatestVersion != "0.5.0" {
		t.Errorf("cache hit: LatestVersion = %q, want 0.5.0", r.LatestVersion)
	}
	if !r.UpdateAvailable {
		t.Errorf("cache hit: expected UpdateAvailable=true")
	}
}

func TestCheck_CacheMiss_FetchesAndWrites(t *testing.T) {
	var hits int32
	withServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(`{"tag_name":"v0.3.0"}`))
	}))
	dir := t.TempDir()

	r := Check(context.Background(), "0.1.0", dir)
	if r.LatestVersion != "0.3.0" {
		t.Fatalf("expected fetched LatestVersion=0.3.0, got %+v", r)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("expected 1 server hit, got %d", hits)
	}

	// Cache file should now exist with the fetched version.
	b, err := os.ReadFile(filepath.Join(dir, "update-check.json"))
	if err != nil {
		t.Fatalf("cache file not written: %v", err)
	}
	var got cacheEntry
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("cache JSON malformed: %v", err)
	}
	if got.LatestVersion != "0.3.0" {
		t.Errorf("cache LatestVersion = %q, want 0.3.0", got.LatestVersion)
	}
	if time.Since(got.CheckedAt) > time.Minute {
		t.Errorf("cache CheckedAt looks stale: %v", got.CheckedAt)
	}
}

func TestCheck_CacheStale_Refetches(t *testing.T) {
	var hits int32
	withServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(`{"tag_name":"v9.9.9"}`))
	}))
	dir := t.TempDir()
	stale := cacheEntry{
		CheckedAt:     time.Now().UTC().Add(-48 * time.Hour),
		LatestVersion: "0.0.1",
	}
	b, _ := json.Marshal(stale)
	_ = os.WriteFile(filepath.Join(dir, "update-check.json"), b, 0o600)

	r := Check(context.Background(), "0.1.0", dir)
	if r.LatestVersion != "9.9.9" {
		t.Errorf("stale cache should be ignored; got LatestVersion=%q", r.LatestVersion)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("expected 1 fetch, got %d", hits)
	}
}

func TestCheck_MalformedCache_FallsThroughToFetch(t *testing.T) {
	var hits int32
	withServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.3"}`))
	}))
	dir := t.TempDir()
	// Garbage that won't unmarshal as cacheEntry.
	_ = os.WriteFile(filepath.Join(dir, "update-check.json"), []byte("not json at all"), 0o600)

	r := Check(context.Background(), "0.1.0", dir)
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("expected malformed cache to trigger fetch; hits=%d", hits)
	}
	if r.LatestVersion != "1.2.3" {
		t.Errorf("malformed cache fallback: LatestVersion = %q, want 1.2.3", r.LatestVersion)
	}
}

func TestCheck_HTTP500_ReturnsZero(t *testing.T) {
	withServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	r := Check(context.Background(), "0.1.0", t.TempDir())
	if r.UpdateAvailable || r.LatestVersion != "" {
		t.Errorf("expected zero Result on 500, got %+v", r)
	}
	if got := FormatNotice(r, false); got != "" {
		t.Errorf("expected no notice on 500, got %q", got)
	}
}

func TestCheck_ConnectionRefused_ReturnsZero(t *testing.T) {
	// Bind a port, immediately close it — subsequent dials connection-refuse.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	orig := ReleasesAPIURL
	ReleasesAPIURL = "http://" + addr + "/releases"
	t.Cleanup(func() { ReleasesAPIURL = orig })

	r := Check(context.Background(), "0.1.0", t.TempDir())
	if r.UpdateAvailable || r.LatestVersion != "" {
		t.Errorf("expected zero Result on connection refused, got %+v", r)
	}
}

func TestCheck_Timeout_ReturnsZero(t *testing.T) {
	// Server that hangs forever — Check's internal timeout should fire.
	withServer(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	// Use a short ctx deadline so the test doesn't take 5s.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	r := Check(ctx, "0.1.0", t.TempDir())
	if r.UpdateAvailable || r.LatestVersion != "" {
		t.Errorf("expected zero Result on timeout, got %+v", r)
	}
}

func TestCheck_DevVersion_SkippedEarly(t *testing.T) {
	withServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("server hit even though current=dev")
	}))
	r := Check(context.Background(), "dev", t.TempDir())
	if r.LatestVersion != "" {
		t.Errorf("expected zero Result for dev version, got %+v", r)
	}
}

func TestCheck_BadCurrentVersion_SkipsCheck(t *testing.T) {
	withServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("server hit even though current is unparseable")
	}))
	r := Check(context.Background(), "garbage", t.TempDir())
	if r.LatestVersion != "" {
		t.Errorf("expected zero Result for bad current version, got %+v", r)
	}
}

func TestCheck_BadRemoteVersion_NoNotice(t *testing.T) {
	withServer(t, jsonHandler(`{"tag_name":"not-a-version"}`))
	r := Check(context.Background(), "0.1.0", t.TempDir())
	if r.UpdateAvailable {
		t.Errorf("expected no update when remote version is unparseable, got %+v", r)
	}
	if got := FormatNotice(r, false); got != "" {
		t.Errorf("expected no notice with unparseable remote, got %q", got)
	}
}

func TestCheck_EmptyTagName_NoNotice(t *testing.T) {
	withServer(t, jsonHandler(`{"tag_name":""}`))
	r := Check(context.Background(), "0.1.0", t.TempDir())
	if r.LatestVersion != "" {
		t.Errorf("expected zero Result for empty tag, got %+v", r)
	}
}

func TestFormatNotice_TTYHasDimEscapes(t *testing.T) {
	r := Result{CurrentVersion: "0.1.0", LatestVersion: "0.2.0", UpdateAvailable: true}
	got := FormatNotice(r, true)
	if !strings.Contains(got, "\033[2m") || !strings.Contains(got, "\033[0m") {
		t.Errorf("expected dim escapes on TTY, got %q", got)
	}
}

func TestFormatNotice_NonTTYPlain(t *testing.T) {
	r := Result{CurrentVersion: "0.1.0", LatestVersion: "0.2.0", UpdateAvailable: true}
	got := FormatNotice(r, false)
	if strings.Contains(got, "\033[") {
		t.Errorf("non-TTY notice should be plain, got %q", got)
	}
}

func TestFormatNotice_EmptyWhenNoUpdate(t *testing.T) {
	cases := []Result{
		{},
		{CurrentVersion: "0.1.0", LatestVersion: "0.1.0", UpdateAvailable: false},
		{CurrentVersion: "0.1.0", LatestVersion: "", UpdateAvailable: false},
	}
	for i, r := range cases {
		if got := FormatNotice(r, true); got != "" {
			t.Errorf("case %d: expected empty notice, got %q", i, got)
		}
	}
}

func TestMaybeNotify_WritesWhenUpdateAvailable(t *testing.T) {
	var buf bytes.Buffer
	r := Result{CurrentVersion: "0.1.0", LatestVersion: "0.2.0", UpdateAvailable: true}
	if err := MaybeNotify(&buf, r, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "0.1.0 → 0.2.0") {
		t.Errorf("expected notice content, got %q", buf.String())
	}
}

func TestMaybeNotify_SilentWhenNoUpdate(t *testing.T) {
	var buf bytes.Buffer
	if err := MaybeNotify(&buf, Result{}, false); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output, got %q", buf.String())
	}
}

func TestShouldSkip(t *testing.T) {
	type tc struct {
		name string
		in   Conditions
		want SkipReason
	}
	emptyEnv := func(string) string { return "" }

	cases := []tc{
		{
			name: "default conditions skip non-tty",
			in:   Conditions{CurrentVersion: "0.1.0", Env: emptyEnv, StderrTTY: false},
			want: SkipNonTTY,
		},
		{
			name: "dev version",
			in:   Conditions{CurrentVersion: "dev", Env: emptyEnv, StderrTTY: true},
			want: SkipDevVersion,
		},
		{
			name: "empty version",
			in:   Conditions{CurrentVersion: "", Env: emptyEnv, StderrTTY: true},
			want: SkipDevVersion,
		},
		{
			name: "opt-out resolved by caller",
			in: Conditions{
				CurrentVersion: "0.1.0",
				StderrTTY:      true,
				OptOut:         true,
				Env:            emptyEnv,
			},
			want: SkipOptOut,
		},
		{
			name: "CI env var",
			in: Conditions{
				CurrentVersion: "0.1.0",
				StderrTTY:      true,
				Env: func(k string) string {
					if k == "CI" {
						return "true"
					}
					return ""
				},
			},
			want: SkipCI,
		},
		{
			name: "JSON mode",
			in:   Conditions{CurrentVersion: "0.1.0", JSONMode: true, StderrTTY: true, Env: emptyEnv},
			want: SkipJSON,
		},
		{
			name: "quiet mode",
			in:   Conditions{CurrentVersion: "0.1.0", Quiet: true, StderrTTY: true, Env: emptyEnv},
			want: SkipQuiet,
		},
		{
			name: "all clear -> run",
			in:   Conditions{CurrentVersion: "0.1.0", StderrTTY: true, Env: emptyEnv},
			want: SkipNone,
		},
		{
			name: "opt-out wins over JSON",
			in: Conditions{
				CurrentVersion: "0.1.0",
				JSONMode:       true,
				StderrTTY:      true,
				OptOut:         true,
				Env:            emptyEnv,
			},
			want: SkipOptOut,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ShouldSkip(c.in); got != c.want {
				t.Errorf("ShouldSkip = %v, want %v", got, c.want)
			}
		})
	}
}

func TestIsTruthy(t *testing.T) {
	yes := []string{"1", "true", "TRUE", "yes", "YES", "on", " on "}
	no := []string{"", "0", "false", "no", "off", "asdf"}
	for _, v := range yes {
		if !isTruthy(v) {
			t.Errorf("isTruthy(%q) = false, want true", v)
		}
	}
	for _, v := range no {
		if isTruthy(v) {
			t.Errorf("isTruthy(%q) = true, want false", v)
		}
	}
}

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
		ok   bool
	}{
		{"0.1.0", "0.2.0", -1, true},
		{"0.2.0", "0.1.0", 1, true},
		{"1.2.3", "1.2.3", 0, true},
		{"v1.2.3", "1.2.3", 0, true},
		{"1.0.0", "2.0.0", -1, true},
		{"1.10.0", "1.9.0", 1, true}, // numeric, not lexical
		{"1.0.0-alpha", "1.0.0", -1, true},
		{"1.0.0", "1.0.0-alpha", 1, true},
		{"1.0.0-alpha", "1.0.0-beta", -1, true},
		{"1.0.0-alpha.1", "1.0.0-alpha.2", -1, true},
		{"1.0.0-alpha.1", "1.0.0-alpha.beta", -1, true}, // numeric < alphanumeric
		{"1.0.0+build1", "1.0.0+build2", 0, true},       // build metadata ignored
		{"not-a-version", "1.0.0", 0, false},
		{"1.0.0", "garbage", 0, false},
		{"", "1.0.0", 0, false},
		{"1.0", "1.0.0", 0, false}, // need 3 parts
	}
	for _, c := range cases {
		got, ok := compareSemver(c.a, c.b)
		if ok != c.ok {
			t.Errorf("compareSemver(%q,%q) ok=%v, want %v", c.a, c.b, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("compareSemver(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestNormalizeVersion(t *testing.T) {
	cases := map[string]string{
		"v1.2.3":  "1.2.3",
		"V1.2.3":  "1.2.3",
		" 1.2.3 ": "1.2.3",
		"1.2.3":   "1.2.3",
		"":        "",
	}
	for in, want := range cases {
		if got := normalizeVersion(in); got != want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCacheDir_HonorsXDG(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/custom/cache")
	got := CacheDir()
	if got != filepath.Join("/custom/cache", "emailable") {
		t.Errorf("CacheDir = %q, want /custom/cache/emailable", got)
	}
}

func TestCacheDir_FallsBackToHome(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "/tmp/fakehome")
	got := CacheDir()
	if got != filepath.Join("/tmp/fakehome", ".cache", "emailable") {
		t.Errorf("CacheDir = %q, want /tmp/fakehome/.cache/emailable", got)
	}
}

func TestWriteCache_CreatesParentDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "deeper")
	path := filepath.Join(dir, "update-check.json")
	err := writeCache(path, cacheEntry{CheckedAt: time.Now(), LatestVersion: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestReadCache_MissingFile(t *testing.T) {
	_, ok := readCache(filepath.Join(t.TempDir(), "missing.json"))
	if ok {
		t.Error("expected ok=false for missing file")
	}
}

func TestReadCache_EmptyFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "empty.json")
	_ = os.WriteFile(p, []byte("{}"), 0o600)
	_, ok := readCache(p)
	if ok {
		t.Error("expected ok=false for empty cacheEntry")
	}
}

// TestCheck_DoesNotErrorOnUnwritableCacheDir verifies cache-write failures
// stay silent. We arrange this by passing a path that points at an existing
// regular file rather than a directory — MkdirAll will fail.
func TestCheck_DoesNotErrorOnUnwritableCacheDir(t *testing.T) {
	withServer(t, jsonHandler(`{"tag_name":"v0.2.0"}`))

	tmp := t.TempDir()
	// Create a file where the cache dir should be — os.MkdirAll fails.
	blockPath := filepath.Join(tmp, "cache")
	if err := os.WriteFile(blockPath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := Check(context.Background(), "0.1.0", blockPath)
	// Cache write fails silently; fetched value still returned.
	if r.LatestVersion != "0.2.0" {
		t.Errorf("expected fetched value despite write failure, got %+v", r)
	}
}

// ensure HTTPTimeout doesn't accidentally get bumped to something user-facing.
func TestHTTPTimeout_IsShort(t *testing.T) {
	if HTTPTimeout > 10*time.Second {
		t.Errorf("HTTPTimeout too long: %v", HTTPTimeout)
	}
}

// errReader exists purely so we don't accidentally trip the linter for the
// `errors` import on platforms where every other use happens to be elided.
var _ = errors.New
