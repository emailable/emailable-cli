package ui

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBar_NonTTY_StartStopSilent(t *testing.T) {
	var buf bytes.Buffer
	b := NewBar(&buf, 40)
	b.Start()
	b.Set(10, 100)
	time.Sleep(2 * TickInterval) // would draw frames on a TTY
	b.Set(50, 100)
	b.Stop()

	if buf.Len() != 0 {
		t.Errorf("expected no output on non-TTY, got %q", buf.String())
	}
}

func TestBar_StopWithoutStart_IsNoOp(t *testing.T) {
	var buf bytes.Buffer
	b := NewBar(&buf, 40)
	b.Stop() // must not panic / hang / write
	if buf.Len() != 0 {
		t.Errorf("expected no output, got %q", buf.String())
	}
}

func TestBar_StopIdempotent(t *testing.T) {
	var buf bytes.Buffer
	b := NewBar(&buf, 40)
	b.Start()
	b.Stop()
	b.Stop() // second Stop must not panic on the closed done channel
}

func TestBar_SetConcurrentSafe(t *testing.T) {
	// Exercises the mutex under -race. Multiple goroutines hammer Set
	// and SetMessage while the animation goroutine reads state on each
	// tick.
	var buf bytes.Buffer
	b := NewBar(&buf, 40)
	b.Start()

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				b.Set(base+j, 1000)
				b.SetMessage("Verifying batch")
			}
		}(i * 100)
	}
	wg.Wait()
	b.Stop()
}

// width=0 now means "fit to terminal each frame" rather than a hardcoded
// default — b.width stays 0 (the dynamic sentinel) while the underlying
// progress model is seeded with defaultBarWidth as a fallback for when
// terminal-width measurement fails.
func TestBar_DefaultWidth_IsDynamic(t *testing.T) {
	var buf bytes.Buffer
	b := NewBar(&buf, 0)
	if b.width != 0 {
		t.Errorf("expected b.width = 0 (dynamic sentinel), got %d", b.width)
	}
	if b.prog.Width != defaultBarWidth {
		t.Errorf("expected initial progress model width = %d, got %d", defaultBarWidth, b.prog.Width)
	}
}

// TestBar_FrameRender exercises the pure frame() builder directly so we
// can assert on the rendered output despite the animation goroutine
// being silent on non-TTY writers.
func TestBar_FrameRender(t *testing.T) {
	var buf bytes.Buffer
	b := NewBar(&buf, 40)
	b.SetMessage("Verifying batch abc")

	// First frame (rendered=false): two lines separated by "\n", no
	// cursor-movement prefix.
	f := b.frame(50, 100, 0, "Verifying batch abc", false, false)
	if strings.HasPrefix(f, "\x1b[1F") {
		t.Errorf("first frame should not start with cursor-prev-line: %q", f)
	}
	if !strings.Contains(f, "\n") {
		t.Errorf("frame should contain a newline separator: %q", f)
	}
	stripped := stripANSI(f)
	if !strings.Contains(stripped, "Verifying batch abc") {
		t.Errorf("expected status message in frame, stripped=%q", stripped)
	}
	if !strings.Contains(stripped, "50/100") {
		t.Errorf("expected counter in frame, stripped=%q", stripped)
	}

	// Subsequent frame (rendered=true): begins with the cursor-prev-line
	// and clear-line sequences so the bar updates in place.
	f = b.frame(50, 100, 0, "Verifying batch abc", false, true)
	if !strings.HasPrefix(f, "\x1b[1F\x1b[2K") {
		t.Errorf("redraw frame should start with cursor reset + clear line: %q", f)
	}

	// Completion frame: ✓ glyph in place of spinner.
	f = b.frame(100, 100, 0, "Verified batch abc", true, true)
	stripped = stripANSI(f)
	if !strings.Contains(stripped, "✓") {
		t.Errorf("completion frame should contain check mark, stripped=%q", stripped)
	}
	if !strings.Contains(stripped, "Verified batch abc") {
		t.Errorf("expected status message on completion, stripped=%q", stripped)
	}
}

// stripANSI removes the CSI escape sequences used by the bar so tests
// can assert on the underlying text.
func stripANSI(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\033' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) {
				j++ // skip terminator
			}
			i = j
			continue
		}
		if s[i] == '\r' {
			i++
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

func TestBar_FrameZeroTotal(t *testing.T) {
	var buf bytes.Buffer
	b := NewBar(&buf, 40)
	// Should not panic on total=0.
	f := b.frame(0, 0, 0, "Working", false, false)
	if !strings.Contains(stripANSI(f), "0/0") {
		t.Errorf("expected 0/0 counter, got %q", stripANSI(f))
	}
}

func TestBar_DefaultMessage(t *testing.T) {
	var buf bytes.Buffer
	b := NewBar(&buf, 40)
	// Default message is "Working" — caller can override via SetMessage.
	f := b.frame(0, 100, 0, b.msg, false, false)
	if !strings.Contains(stripANSI(f), "Working") {
		t.Errorf("expected default message, got %q", stripANSI(f))
	}
}
