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

// Bar is a two-line progress display in the style of charmbracelet's
// progress bubble: an animated spinner + status message on the first line,
// and a solid-fill bar with a "processed/total" counter on the second.
//
// Bar is safe for concurrent calls to Set/SetMessage. Start/Stop should
// each be called at most once from the owning goroutine, though Stop is
// idempotent.
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

	// Cached lipgloss styles. Lipgloss's default renderer auto-detects the
	// color profile from stderr; since we explicitly gate on IsTTY before
	// rendering, the styles only emit ANSI on a real terminal.
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
	// The initial progress.Model width is a placeholder; for dynamic
	// (width=0) bars it's overwritten on each frame in frame(). We use
	// defaultBarWidth here so that even before the first measurement we
	// have a sane fallback.
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

// Stop ends the animation and clears both bar lines, leaving the cursor
// at the column the bar started in. The caller is responsible for any
// follow-up output (summary line, etc.) — by erasing itself the bar
// avoids duplicating the completion message that command callers print
// next, matching how tools like mise collapse progress UI on completion.
// Idempotent and safe to call without a prior Start.
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
		// Never drew a frame (Start → Stop with no ticks) — nothing to
		// erase.
		return
	}
	// Cursor sits at the end of line 2 from the last tick. Clear that
	// line, walk up one row, clear line 1, then carriage-return so the
	// next write starts at column 0 in the position the bar occupied.
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

	// Pick the fill width for this frame:
	//   - b.width == 0  → fit as wide as the terminal allows (cap is
	//                     terminal cols minus counter + gutter).
	//   - b.width  > 0  → use that as the desired width, but still cap
	//                     at terminal fit so a fixed 40-cell bar in a
	//                     narrow shell never wraps.
	// terminalWidth is re-measured each frame so SIGWINCH/resize is
	// picked up without a signal handler.
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
		// \x1b[1F: cursor to start of previous line. After printing two
		// lines, the cursor sits at the end of line 2; one [1F brings it
		// to the start of line 1. \x1b[2K clears the entire line.
		buf.WriteString("\x1b[1F\x1b[2K")
		buf.WriteString(line1)
		buf.WriteString("\n\x1b[2K")
		buf.WriteString(line2)
	}
	return buf.String()
}
