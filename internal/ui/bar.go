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

const defaultBarWidth = 50
const minBarWidth = 10

// Bar is a two-line progress display: spinner + message on line 1, solid-fill
// bar + counter on line 2. Set/SetMessage are safe for concurrent use.
type Bar struct {
	w     io.Writer
	width int // 0 = fit to terminal each frame; >0 = fixed width
	noTTY bool

	mu        sync.Mutex
	processed int
	total     int
	spinIdx   int
	msg       string
	rendered  bool

	prog progress.Model

	spinnerStyle lipgloss.Style
	checkStyle   lipgloss.Style
	counterStyle lipgloss.Style
	msgStyle     lipgloss.Style

	started  bool
	done     chan struct{}
	wg       sync.WaitGroup
	stopOnce sync.Once
}

// NewBar returns a Bar writing to w. width=0 fits the terminal each frame;
// a positive width locks to a fixed count (useful for deterministic tests).
func NewBar(w io.Writer, width int) *Bar {
	if width > 0 && width < 4 {
		width = 4
	}
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

// Set updates the processed and total counts used to compute bar progress.
func (b *Bar) Set(processed, total int) {
	b.mu.Lock()
	b.processed = processed
	b.total = total
	b.mu.Unlock()
}

// SetMessage updates the status text shown on the first line.
func (b *Bar) SetMessage(msg string) {
	b.mu.Lock()
	b.msg = msg
	b.mu.Unlock()
}

// Start begins the animation loop. Idempotent.
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

// Stop ends the animation and clears both bar lines. Idempotent.
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
		return
	}
	fmt.Fprint(b.w, "\r\x1b[2K\x1b[1F\x1b[2K")
}

func (b *Bar) renderTick() {
	b.mu.Lock()
	processed, total, spinIdx, msg := b.processed, b.total, b.spinIdx, b.msg
	b.spinIdx++
	rendered := b.rendered
	b.rendered = true
	b.mu.Unlock()
	fmt.Fprint(b.w, b.frame(processed, total, spinIdx, msg, false, rendered))
}

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

	// Right-align processed against total width so digits don't jitter as they grow.
	totalWidth := len(fmt.Sprintf("%d", total))
	counter := b.counterStyle.Render(fmt.Sprintf("%*d/%d", totalWidth, processed, total))

	// Re-measured each frame so terminal resizes are reflected without a signal handler.
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
		buf.WriteString("\x1b[1F\x1b[2K")
		buf.WriteString(line1)
		buf.WriteString("\n\x1b[2K")
		buf.WriteString(line2)
	}
	return buf.String()
}
