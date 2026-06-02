// Package updater implements an unobtrusive "new release available" notifier.
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

// ReleasesAPIURL is the GitHub Releases endpoint we poll; a var so tests can redirect it.
var ReleasesAPIURL = "https://api.github.com/repos/emailable/emailable-cli/releases/latest"

// ReleasesPageURL is the human-facing GitHub Releases page linked in update notices.
const ReleasesPageURL = "https://github.com/emailable/emailable-cli/releases/latest"

// CacheTTL is how long a cached update check is reused before a fresh fetch is made.
const CacheTTL = 24 * time.Hour

// HTTPTimeout is the deadline for a single update-check HTTP request.
const HTTPTimeout = 5 * time.Second

// Result is the outcome of a Check. An empty LatestVersion means "nothing
// useful to say" (silent failure, ambiguous comparison, or already current).
type Result struct {
	CurrentVersion  string
	LatestVersion   string
	UpdateAvailable bool
}

type cacheEntry struct {
	CheckedAt     time.Time `json:"checked_at"`
	LatestVersion string    `json:"latest_version"`
}

// httpClient is a var so tests can swap it for one pointed at an httptest server.
var httpClient = &http.Client{Timeout: HTTPTimeout}

// Check returns the update-check Result for currentVersion, using cacheDir to cache the last fetch.
func Check(ctx context.Context, currentVersion, cacheDir string) Result {
	if currentVersion == "" || currentVersion == "dev" {
		return Result{}
	}

	cur := normalizeVersion(currentVersion)
	if _, ok := parseSemver(cur); !ok {
		return Result{}
	}

	cachePath := filepath.Join(cacheDir, "update-check.json")

	// Still compare on a cache hit so a known upgrade nags every run until the user upgrades.
	if entry, ok := readCache(cachePath); ok && time.Since(entry.CheckedAt) < CacheTTL {
		return buildResult(cur, entry.LatestVersion)
	}

	latest, ok := fetchLatest(ctx)
	if !ok {
		return Result{}
	}

	_ = writeCache(cachePath, cacheEntry{CheckedAt: time.Now().UTC(), LatestVersion: latest})

	return buildResult(cur, latest)
}

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

func fetchLatest(ctx context.Context) (string, bool) {
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
	// 1 MiB cap so a runaway response can't soak memory.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return "", false
	}
	tag := strings.TrimSpace(payload.TagName)
	if tag == "" {
		return "", false
	}
	return normalizeVersion(tag), true
}

func readCache(path string) (cacheEntry, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return cacheEntry{}, false
	}
	var e cacheEntry
	if err := json.Unmarshal(b, &e); err != nil {
		return cacheEntry{}, false
	}
	if e.CheckedAt.IsZero() && e.LatestVersion == "" {
		return cacheEntry{}, false
	}
	return e, true
}

func writeCache(path string, e cacheEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// SkipReason describes why ShouldSkip decided to suppress the update check.
type SkipReason int

const (
	// SkipNone means the update check should proceed.
	SkipNone SkipReason = iota
	// SkipDevVersion skips when running a dev or unversioned build.
	SkipDevVersion
	// SkipOptOut skips when the user has opted out.
	SkipOptOut
	// SkipCI skips when the CI environment variable is set.
	SkipCI
	// SkipJSON skips in JSON output mode to avoid polluting machine output.
	SkipJSON
	// SkipQuiet skips when quiet mode is active.
	SkipQuiet
	// SkipNonTTY skips when stderr is not a terminal.
	SkipNonTTY
)

// Conditions is the set of runtime knobs ShouldSkip inspects. All are passed
// in rather than read from globals so tests can drive every branch.
type Conditions struct {
	CurrentVersion string
	JSONMode       bool
	Quiet          bool
	StderrTTY      bool
	OptOut         bool
	Env            func(string) string // nil => os.Getenv
}

// ShouldSkip returns the first SkipReason that applies to c, or SkipNone.
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

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// FormatNotice returns a human-readable update notice for r, or an empty string if no update is available.
func FormatNotice(r Result, tty bool) string {
	if !r.UpdateAvailable || r.CurrentVersion == "" || r.LatestVersion == "" {
		return ""
	}
	line1 := fmt.Sprintf("A new release of emailable is available: %s → %s", r.CurrentVersion, r.LatestVersion)
	line2 := ReleasesPageURL
	if !tty {
		return "\n" + line1 + "\n" + line2 + "\n"
	}
	const dim = "\033[2m"
	const reset = "\033[0m"
	return "\n" + dim + line1 + reset + "\n" + dim + line2 + reset + "\n"
}

// MaybeNotify writes a formatted update notice to w if r indicates an available update.
func MaybeNotify(w io.Writer, r Result, tty bool) error {
	notice := FormatNotice(r, tty)
	if notice == "" {
		return nil
	}
	_, err := io.WriteString(w, notice)
	return err
}

// CacheDir returns the platform-appropriate cache directory for update-check state.
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

// semver is a parsed version. Pre is empty for stable releases, which sort
// after any same-MMP pre-release per semver §11.4.
type semver struct {
	Major, Minor, Patch int
	Pre                 string
}

func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "v") || strings.HasPrefix(v, "V") {
		v = v[1:]
	}
	return v
}

// parseSemver parses "MAJOR.MINOR.PATCH[-pre][+build]"; build metadata is
// discarded per semver §10 (doesn't affect precedence).
func parseSemver(v string) (semver, bool) {
	v = normalizeVersion(v)
	if v == "" {
		return semver{}, false
	}
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
	// semver §11.4: stable release sorts after any same-MMP pre-release.
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
			return -1 // semver §11.4: numeric < alphanumeric
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

// cmpInt returns -1/0/1; subtraction-and-sign risks overflow on large ints.
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
