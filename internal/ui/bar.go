package ui

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/lipgloss"
)

// defaultBarWidth is the progress-bar fill width (number of cells) used
// as a fallback when the terminal size can't be measured.
const defaultBarWidth = 50

// minBarWidth is the floor for the dynamic fit: even on absurdly narrow
// terminals we keep the bar drawable.
const minBarWidth = 10

// Bar is a two-line progress display: an animated spinner + status message on
// line 1, a solid-fill bar with a "processed/total" counter on line 2.
//
// Set/SetMessage are safe for concurrent use. Start/Stop should each be called
// at most once from the owning goroutine, though Stop is idempotent.
type Bar struct {
	w io.Writer
	// width, when > 0, locks the bar to a fixed cell count. When 0
	// (the production default) the bar fits to the terminal width each
	// frame so it visually fills the screen — mirroring tools like mise.
	width int
	noTTY bool

	mu        sync.Mutex
	processed int
	total     int
	spinIdx   int
	msg       string
	rendered  bool // false until the first frame has been printed

	prog progress.Model

	// Cached lipgloss styles. We gate on IsTTY before rendering, so these
	// only emit ANSI on a real terminal regardless of lipgloss's own probe.
	spinnerStyle lipgloss.Style
	checkStyle   lipgloss.Style
	counterStyle lipgloss.Style
	msgStyle     lipgloss.Style

	started  bool
	done     chan struct{}
	wg       sync.WaitGroup
	stopOnce sync.Once
}

// NewBar returns a Bar that writes to w. Pass width=0 (the production
// default) to fit the bar to the terminal width on every frame. Pass an
// explicit positive width to lock the bar to a fixed cell count — useful
// for tests where deterministic output matters.
func NewBar(w io.Writer, width int) *Bar {
	if width > 0 && width < 4 {
		width = 4
	}
	// For dynamic (width=0) bars this is just a fallback before the first
	// per-frame measurement.
	initialWidth := width
	if initialWidth == 0 {
		initialWidth = defaultBarWidth
	}
	p := progress.New(
		progress.WithSolidFill("63"),
		progress.WithWidth(initialWidth),
		progress.WithoutPercentage(),
	)
	return &Bar{
		w:            w,
		width:        width,
		noTTY:        !IsTTY(w),
		msg:          "Working",
		prog:         p,
		spinnerStyle: SpinnerStyle,
		checkStyle:   lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true),
		counterStyle: lipgloss.NewStyle().Foreground(lipgloss.Color("241")),
		msgStyle:     lipgloss.NewStyle(),
		done:         make(chan struct{}),
	}
}

// Set updates the bar's current processed/total counts. Safe to call
// from any goroutine; the next animation tick (or the final frame
// written by Stop) reflects the new values.
func (b *Bar) Set(processed, total int) {
	b.mu.Lock()
	b.processed = processed
	b.total = total
	b.mu.Unlock()
}

// SetMessage updates the status message shown on the spinner line. Safe
// to call from any goroutine.
func (b *Bar) SetMessage(msg string) {
	b.mu.Lock()
	b.msg = msg
	b.mu.Unlock()
}

// Start begins the animation goroutine. On a non-TTY writer, Start is a
// no-op and Stop will likewise be silent.
func (b *Bar) Start() {
	b.mu.Lock()
	if b.started {
		b.mu.Unlock()
		return
	}
	b.started = true
	b.mu.Unlock()

	if b.noTTY {
		return
	}

	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		ticker := time.NewTicker(TickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				b.renderTick()
			case <-b.done:
				return
			}
		}
	}()
}

// Stop ends the animation and clears both bar lines, leaving the cursor at the
// column the bar started in so the caller's follow-up output (summary line,
// etc.) isn't duplicated. Idempotent and safe to call without a prior Start.
func (b *Bar) Stop() {
	b.mu.Lock()
	started := b.started
	rendered := b.rendered
	b.mu.Unlock()
	if !started || b.noTTY {
		return
	}
	b.stopOnce.Do(func() {
		close(b.done)
	})
	b.wg.Wait()

	if !rendered {
		// Never drew a frame — nothing to erase.
		return
	}
	// Clear line 2, move up one row, clear line 1, return to column 0.
	fmt.Fprint(b.w, "\r\x1b[2K\x1b[1F\x1b[2K")
}

// renderTick reads state under the lock, advances the spinner index,
// and writes one frame.
func (b *Bar) renderTick() {
	b.mu.Lock()
	processed, total, spinIdx, msg := b.processed, b.total, b.spinIdx, b.msg
	b.spinIdx++
	rendered := b.rendered
	b.rendered = true
	b.mu.Unlock()
	fmt.Fprint(b.w, b.frame(processed, total, spinIdx, msg, false, rendered))
}

// frame builds the two-line rendered output for the given state.
//
// On the very first frame (rendered=false) it prints both lines outright;
// subsequent frames first move the cursor back to the start of line 1
// and clear each line before reprinting, so the bar updates in place.
func (b *Bar) frame(processed, total, spinIdx int, msg string, done, rendered bool) string {
	pct := 0.0
	if total > 0 {
		pct = float64(processed) / float64(total)
		if pct > 1 {
			pct = 1
		}
		if pct < 0 {
			pct = 0
		}
	}
	if done {
		pct = 1
	}

	var glyph string
	if done {
		glyph = b.checkStyle.Render("✓")
	} else {
		spinChar := Frames[((spinIdx%len(Frames))+len(Frames))%len(Frames)]
		glyph = b.spinnerStyle.Render(string(spinChar))
	}

	line1 := glyph + " " + b.msgStyle.Render(msg)

	// Width of the largest count so the digits don't jitter as they grow.
	totalWidth := len(fmt.Sprintf("%d", total))
	counter := b.counterStyle.Render(fmt.Sprintf("%*d/%d", totalWidth, processed, total))

	// Fill width: width==0 fits the terminal; width>0 is the desired width
	// but still capped at terminal fit so it never wraps. Re-measured each
	// frame so resizes are picked up without a signal handler.
	target := b.width
	if cols := terminalWidth(b.w); cols > 0 {
		fit := cols - lipgloss.Width(counter) - 2
		if target == 0 || fit < target {
			target = fit
		}
	}
	if target > 0 {
		if target < minBarWidth {
			target = minBarWidth
		}
		b.prog.Width = target
	}

	line2 := b.prog.ViewAs(pct) + "  " + counter

	var buf strings.Builder
	if !rendered {
		buf.WriteString(line1)
		buf.WriteString("\n")
		buf.WriteString(line2)
	} else {
		// Move to the start of line 1 (\x1b[1F) and clear each line
		// (\x1b[2K) before reprinting, so the bar updates in place.
		buf.WriteString("\x1b[1F\x1b[2K")
		buf.WriteString(line1)
		buf.WriteString("\n\x1b[2K")
		buf.WriteString(line2)
	}
	return buf.String()
}
