package cmd

import (
	"runtime"
	"testing"
)

// TestOpenBrowser_Unsupported verifies the helper returns an error for an
// unknown GOOS. We can't usefully assert success on the current platform
// without actually launching a browser, so we only cover the negative case.
//
// End-to-end coverage for the full login flow is intentionally omitted: the
// command reads from os.Stdin and spawns a browser process, neither of which
// is comfortable to drive from a hermetic unit test.
func TestOpenBrowser_Unsupported(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" ||
		runtime.GOOS == "windows" || runtime.GOOS == "freebsd" ||
		runtime.GOOS == "openbsd" || runtime.GOOS == "netbsd" {
		t.Skip("openBrowser supports this GOOS; nothing to assert for the unsupported branch")
	}
	if err := openBrowser("https://example.com"); err == nil {
		t.Errorf("expected error on unsupported platform %q, got nil", runtime.GOOS)
	}
}
