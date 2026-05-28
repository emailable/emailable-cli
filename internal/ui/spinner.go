// Package ui holds shared terminal-UI primitives (spinner, progress bar) so
// every animated wait in the CLI uses the same cadence and styling.
package ui

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// noColorEnv is the env var name from https://no-color.org/. Any non-empty
// value suppresses ANSI styling even on a real TTY.
const noColorEnv = "NO_COLOR"

// SpinnerStyle is the shared style for the spinner glyph so it reads the same
// everywhere; changing the color here changes every spinner at once.
var SpinnerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("69"))

// Frames is the Braille spinner used for every animated wait in the CLI.
var Frames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// TickInterval is the redraw cadence (~10 fps).
const TickInterval = 100 * time.Millisecond

// IsTTY reports whether w writes to a terminal AND color/animation should be
// enabled, gating ANSI output so pipes don't fill with control codes.
//
// Honors the NO_COLOR convention (https://no-color.org/): a non-empty NO_COLOR
// env var returns false even on a real terminal.
func IsTTY(w io.Writer) bool {
	if os.Getenv(noColorEnv) != "" {
		return false
	}
	return isTerminal(w)
}

// isTerminal is the pure file-descriptor check. A var so tests can swap in a
// fake TTY.
var isTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// IsTerminal reports whether w is a terminal, ignoring NO_COLOR. Use it for
// interactivity decisions; IsTTY (which honors NO_COLOR) is for styling.
func IsTerminal(w io.Writer) bool {
	return isTerminal(w)
}

// terminalWidth returns the column count of the terminal w is writing to,
// or 0 if w isn't a TTY or the size can't be determined. Re-measured on
// every frame so the progress bar tracks terminal resizes.
func terminalWidth(w io.Writer) int {
	f, ok := w.(*os.File)
	if !ok {
		return 0
	}
	cols, _, err := term.GetSize(int(f.Fd()))
	if err != nil || cols <= 0 {
		return 0
	}
	return cols
}

// Spinner is a single-line animated status indicator. It writes to stderr by
// default and degrades to a single status print when stderr is not a TTY.
type Spinner struct {
	w   io.Writer
	noP bool // true => not a TTY; suppress animation, fall back to a single print

	mu  sync.Mutex
	msg string

	started  bool
	done     chan struct{}
	wg       sync.WaitGroup
	stopOnce sync.Once
}

// New returns a Spinner that writes to stderr with the given initial message.
func New(message string) *Spinner {
	return NewTo(os.Stderr, message)
}

// NewTo returns a Spinner that writes to w. Used by tests that want a buffer.
func NewTo(w io.Writer, message string) *Spinner {
	s := &Spinner{
		w:    w,
		msg:  message,
		done: make(chan struct{}),
	}
	s.noP = !IsTTY(w)
	return s
}

// SetMessage updates the message rendered next to the spinner. Safe from any
// goroutine.
func (s *Spinner) SetMessage(msg string) {
	s.mu.Lock()
	s.msg = msg
	s.mu.Unlock()
}

// Start begins the animation. When the writer is not a TTY, prints the
// message once and returns; Stop is then a no-op.
func (s *Spinner) Start() {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	msg := s.msg
	s.mu.Unlock()

	if s.noP {
		fmt.Fprintln(s.w, msg+"...")
		return
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(TickInterval)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-ticker.C:
				s.mu.Lock()
				m := s.msg
				s.mu.Unlock()
				// \r + \033[K clears the line so a shorter message doesn't
				// leave stale characters from a longer previous one.
				glyph := SpinnerStyle.Render(string(Frames[i%len(Frames)]))
				fmt.Fprintf(s.w, "\r\033[K%s %s", glyph, m)
				i++
			case <-s.done:
				return
			}
		}
	}()
}

// Stop ends the animation and clears the spinner line. Idempotent.
func (s *Spinner) Stop() {
	s.mu.Lock()
	started := s.started
	s.mu.Unlock()
	if !started || s.noP {
		return
	}
	s.stopOnce.Do(func() {
		close(s.done)
	})
	s.wg.Wait()
	fmt.Fprint(s.w, "\r\033[K")
}
