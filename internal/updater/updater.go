// Package updater implements an unobtrusive "new release available" notifier.
// It checks GitHub for the latest release at most once per 24h, caches the
// answer on disk, and prints a short two-line stderr notice (version + release
// URL) when an update exists.
//
// The notifier must never block or fail the user's command: the check runs in
// a goroutine, the caller waits at most ~1s for it before exiting, and any
// error is swallowed silently. All I/O is injectable for hermetic tests.
package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ReleasesAPIURL is the GitHub Releases endpoint we poll. Exposed as a var so
// tests can point it at an httptest.Server.
var ReleasesAPIURL = "https://api.github.com/repos/emailable/emailable-cli/releases/latest"

// ReleasesPageURL is the human-facing URL printed in the notice. Tests don't
// override this — it just gets echoed into the notice string verbatim.
const ReleasesPageURL = "https://github.com/emailable/emailable-cli/releases/latest"

// CacheTTL is how long a cache hit suppresses the network call. 24h so users
// see updates roughly daily without a check every run.
const CacheTTL = 24 * time.Hour

// HTTPTimeout caps every GitHub API request, short enough that a hung
// connection won't materially delay process exit.
const HTTPTimeout = 5 * time.Second

// Result is the outcome of a Check. An empty LatestVersion means "nothing
// useful to say" (silent failure, ambiguous comparison, or already current).
type Result struct {
	// CurrentVersion is the caller's version, leading "v" stripped.
	CurrentVersion string
	// LatestVersion is the latest release tag, leading "v" stripped. Empty
	// when there's nothing to report.
	LatestVersion string
	// UpdateAvailable is true iff LatestVersion > CurrentVersion. False when
	// versions match, either side fails to parse, or no fetch was attempted.
	UpdateAvailable bool
}

// cacheEntry is the on-disk schema: timestamp + last-seen version.
type cacheEntry struct {
	CheckedAt     time.Time `json:"checked_at"`
	LatestVersion string    `json:"latest_version"`
}

// httpClient is the package-level HTTP client. Exposed as a var so tests can
// swap it out for one pointed at an httptest server with no real network.
var httpClient = &http.Client{Timeout: HTTPTimeout}

// Check returns the latest release info, using and updating a 24h disk cache
// under cacheDir. currentVersion is the running binary's version (leading "v"
// optional). Any error yields a zero Result: the notifier is best-effort and
// must never fail the caller. ctx bounds the whole operation.
func Check(ctx context.Context, currentVersion, cacheDir string) Result {
	if currentVersion == "" || currentVersion == "dev" {
		return Result{}
	}

	cur := normalizeVersion(currentVersion)
	if _, ok := parseSemver(cur); !ok {
		return Result{}
	}

	cachePath := filepath.Join(cacheDir, "update-check.json")

	// Fresh cache hit: still compare so a known upgrade nags every run until
	// the user upgrades.
	if entry, ok := readCache(cachePath); ok && time.Since(entry.CheckedAt) < CacheTTL {
		return buildResult(cur, entry.LatestVersion)
	}

	latest, ok := fetchLatest(ctx)
	if !ok {
		// Preserve any stale entry on failure so an offline run doesn't lose
		// the previously known version.
		return Result{}
	}

	// Best-effort cache write; a failure just means we re-fetch next time.
	_ = writeCache(cachePath, cacheEntry{CheckedAt: time.Now().UTC(), LatestVersion: latest})

	return buildResult(cur, latest)
}

// buildResult assembles a Result from normalized version strings. Returns a
// zero Result when latest is empty or either side fails semver parse. Equal
// versions populate the fields with UpdateAvailable=false, distinguishing
// "checked, up to date" from "nothing to report".
func buildResult(current, latest string) Result {
	latest = normalizeVersion(latest)
	if latest == "" {
		return Result{}
	}
	cmp, ok := compareSemver(current, latest)
	if !ok {
		return Result{}
	}
	return Result{
		CurrentVersion:  current,
		LatestVersion:   latest,
		UpdateAvailable: cmp < 0,
	}
}

// fetchLatest GETs the releases endpoint and returns the tag_name, leading
// "v" stripped. Returns ok=false on any error so callers can skip uniformly.
func fetchLatest(ctx context.Context) (string, bool) {
	// Internal timeout on top of ctx so a context.Background() caller still
	// gets bounded I/O.
	rctx, cancel := context.WithTimeout(ctx, HTTPTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(rctx, http.MethodGet, ReleasesAPIURL, nil)
	if err != nil {
		return "", false
	}
	// GitHub rejects requests with no User-Agent.
	req.Header.Set("User-Agent", "emailable-cli-update-check")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	// Cap at 1 MiB so a runaway response can't soak memory.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return "", false
	}
	tag := strings.TrimSpace(payload.TagName)
	if tag == "" {
		return "", false
	}
	return normalizeVersion(tag), true
}

// readCache loads the cache file at path. Returns ok=false on any error so
// the caller falls through to a fresh fetch.
func readCache(path string) (cacheEntry, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return cacheEntry{}, false
	}
	var e cacheEntry
	if err := json.Unmarshal(b, &e); err != nil {
		return cacheEntry{}, false
	}
	// Treat a wholly empty entry as a miss so it can't mask a fetch error.
	if e.CheckedAt.IsZero() && e.LatestVersion == "" {
		return cacheEntry{}, false
	}
	return e, true
}

// writeCache persists e to path, creating the parent directory if needed.
// Returns the error for callers that want it; production callers ignore it.
func writeCache(path string, e cacheEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	// 0600: nothing here is secret, but tighter perms cost nothing.
	return os.WriteFile(path, b, 0o600)
}

// SkipReason represents why the notifier opted out on this invocation.
type SkipReason int

const (
	// SkipNone means no skip condition matched; the notifier should run.
	SkipNone SkipReason = iota
	// SkipDevVersion is set for the "dev" version (don't nag local checkouts).
	SkipDevVersion
	// SkipOptOut is set when the opt-out env var is truthy.
	SkipOptOut
	// SkipCI is set when the CI env var is non-empty.
	SkipCI
	// SkipJSON is set in --json mode (no stderr line in machine output).
	SkipJSON
	// SkipQuiet is set when --quiet is active.
	SkipQuiet
	// SkipNonTTY is set when stderr isn't a terminal.
	SkipNonTTY
)

// Conditions is the set of runtime knobs ShouldSkip inspects. All sources are
// passed in (not read from globals) so tests can drive every branch.
type Conditions struct {
	// CurrentVersion is the running binary's version string.
	CurrentVersion string
	// JSONMode is true when output is in JSON mode.
	JSONMode bool
	// Quiet is true when --quiet/-q suppresses the notice.
	Quiet bool
	// StderrTTY is true when stderr is a terminal; false suppresses the notice.
	StderrTTY bool
	// OptOut is the resolved opt-out signal, computed by the caller so this
	// package doesn't read the environment for it.
	OptOut bool
	// Env reads environment variables; injectable for tests. nil means
	// os.Getenv. Currently only the CI check uses it.
	Env func(string) string
}

// ShouldSkip returns the first matching skip reason, or SkipNone if all
// skip conditions are false (i.e. the notifier should proceed).
func ShouldSkip(c Conditions) SkipReason {
	getenv := c.Env
	if getenv == nil {
		getenv = os.Getenv
	}
	if c.CurrentVersion == "" || c.CurrentVersion == "dev" {
		return SkipDevVersion
	}
	if c.OptOut {
		return SkipOptOut
	}
	if getenv("CI") != "" {
		return SkipCI
	}
	if c.JSONMode {
		return SkipJSON
	}
	if c.Quiet {
		return SkipQuiet
	}
	if !c.StderrTTY {
		return SkipNonTTY
	}
	return SkipNone
}

// isTruthy returns true for "1", "true", "yes", "on" (case-insensitive).
// Empty / "0" / "false" return false. Keeps the opt-out env var behaviour
// predictable for users who type the obvious values.
func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// FormatNotice renders the dim two-line notice. When tty is false the output
// is plain text. Returns empty string when no notice should be printed (no
// update, missing versions, etc.) so callers can unconditionally print the
// return value.
func FormatNotice(r Result, tty bool) string {
	if !r.UpdateAvailable || r.CurrentVersion == "" || r.LatestVersion == "" {
		return ""
	}
	// Leading blank line separates the notice from the command's output.
	line1 := fmt.Sprintf("A new release of emailable is available: %s → %s", r.CurrentVersion, r.LatestVersion)
	line2 := ReleasesPageURL
	if !tty {
		return "\n" + line1 + "\n" + line2 + "\n"
	}
	const dim = "\033[2m"
	const reset = "\033[0m"
	return "\n" + dim + line1 + reset + "\n" + dim + line2 + reset + "\n"
}

// MaybeNotify is the convenience top-level entry point: combine a Result and
// a writer, and print the notice (if any) honoring TTY/color detection. Only
// writes to w when there's something to say. Returns nil on a no-op.
func MaybeNotify(w io.Writer, r Result, tty bool) error {
	notice := FormatNotice(r, tty)
	if notice == "" {
		return nil
	}
	_, err := io.WriteString(w, notice)
	return err
}

// CacheDir returns the update-check cache directory, honoring $XDG_CACHE_HOME
// and falling back to ~/.cache. Returns "" if neither resolves (the caller
// should then skip the cache).
func CacheDir() string {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "emailable")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".cache", "emailable")
}

// --- Semver comparator -------------------------------------------------------
//
// Hand-rolled to avoid pulling in golang.org/x/mod/semver. It only needs the
// canonical X.Y.Z[-pre][+build] form GitHub release tags use, covering the
// one comparison we do: "is latest > current?"

// semver is a parsed version. Pre is the dot-separated pre-release portion
// (without the leading "-"); empty means a stable release, which by semver
// rules sorts after any same-MMP pre-release.
type semver struct {
	Major, Minor, Patch int
	Pre                 string
}

// normalizeVersion strips a leading "v" and any whitespace. Empty input
// returns empty.
func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "v") || strings.HasPrefix(v, "V") {
		v = v[1:]
	}
	return v
}

// parseSemver parses "MAJOR.MINOR.PATCH[-pre][+build]". Returns ok=false on
// any malformed input. Build metadata is discarded (it doesn't affect
// precedence per semver §10).
func parseSemver(v string) (semver, bool) {
	v = normalizeVersion(v)
	if v == "" {
		return semver{}, false
	}
	// Strip build metadata.
	if i := strings.Index(v, "+"); i >= 0 {
		v = v[:i]
	}
	var pre string
	if i := strings.Index(v, "-"); i >= 0 {
		pre = v[i+1:]
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	maj, err := strconv.Atoi(parts[0])
	if err != nil || maj < 0 {
		return semver{}, false
	}
	min, err := strconv.Atoi(parts[1])
	if err != nil || min < 0 {
		return semver{}, false
	}
	pat, err := strconv.Atoi(parts[2])
	if err != nil || pat < 0 {
		return semver{}, false
	}
	return semver{Major: maj, Minor: min, Patch: pat, Pre: pre}, true
}

// compareSemver returns -1/0/1 for a < b / a == b / a > b. ok=false when
// either side fails to parse — the caller then suppresses the notice
// rather than guess.
func compareSemver(a, b string) (int, bool) {
	sa, oka := parseSemver(a)
	sb, okb := parseSemver(b)
	if !oka || !okb {
		return 0, false
	}
	if c := cmpInt(sa.Major, sb.Major); c != 0 {
		return c, true
	}
	if c := cmpInt(sa.Minor, sb.Minor); c != 0 {
		return c, true
	}
	if c := cmpInt(sa.Patch, sb.Patch); c != 0 {
		return c, true
	}
	// Same MAJOR.MINOR.PATCH: pre-release rules (semver §11.4):
	//   - a stable release > any pre-release of same MMP
	//   - pre-release identifiers compared lexically segment-by-segment
	switch {
	case sa.Pre == "" && sb.Pre == "":
		return 0, true
	case sa.Pre == "":
		return 1, true
	case sb.Pre == "":
		return -1, true
	}
	return cmpPreRelease(sa.Pre, sb.Pre), true
}

// cmpPreRelease compares two non-empty pre-release strings. Numeric
// identifiers compare numerically; alphanumeric compare lexically; numeric
// identifiers have lower precedence than alphanumeric (semver §11.4).
func cmpPreRelease(a, b string) int {
	ap := strings.Split(a, ".")
	bp := strings.Split(b, ".")
	for i := 0; i < len(ap) && i < len(bp); i++ {
		an, aErr := strconv.Atoi(ap[i])
		bn, bErr := strconv.Atoi(bp[i])
		aNum := aErr == nil
		bNum := bErr == nil
		switch {
		case aNum && bNum:
			if c := cmpInt(an, bn); c != 0 {
				return c
			}
		case aNum:
			// Numeric identifiers have lower precedence than alphanumeric.
			return -1
		case bNum:
			return 1
		default:
			if ap[i] < bp[i] {
				return -1
			}
			if ap[i] > bp[i] {
				return 1
			}
		}
	}
	return cmpInt(len(ap), len(bp))
}

// cmpInt is a -1/0/1 comparator over ints (subtract-and-sign risks overflow).
func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
