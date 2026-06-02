// Package ui holds shared terminal-UI primitives for animated CLI output.
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

// noColorEnv — see https://no-color.org/
const noColorEnv = "NO_COLOR"

// SpinnerStyle is the shared style for the spinner glyph.
var SpinnerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("69"))

// Frames are the spinner's animation frames.
var Frames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// TickInterval is the spinner's redraw cadence.
const TickInterval = 100 * time.Millisecond

// IsTTY reports whether ANSI output is appropriate for w (real TTY + NO_COLOR not set).
func IsTTY(w io.Writer) bool {
	if os.Getenv(noColorEnv) != "" {
		return false
	}
	return isTerminal(w)
}

// isTerminal is a var so tests can swap in a fake TTY.
var isTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// IsTerminal checks the fd only, ignoring NO_COLOR. Use for interactivity; use IsTTY for styling.
func IsTerminal(w io.Writer) bool {
	return isTerminal(w)
}

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

// Spinner is a single-line animated status indicator; degrades to a single print on non-TTY.
type Spinner struct {
	w   io.Writer
	noP bool

	mu  sync.Mutex
	msg string

	started  bool
	done     chan struct{}
	wg       sync.WaitGroup
	stopOnce sync.Once
}

// New returns a Spinner that writes to stderr.
func New(message string) *Spinner {
	return NewTo(os.Stderr, message)
}

// NewTo returns a Spinner that writes to w.
func NewTo(w io.Writer, message string) *Spinner {
	s := &Spinner{
		w:    w,
		msg:  message,
		done: make(chan struct{}),
	}
	s.noP = !IsTTY(w)
	return s
}

// SetMessage updates the message shown next to the spinner.
func (s *Spinner) SetMessage(msg string) {
	s.mu.Lock()
	s.msg = msg
	s.mu.Unlock()
}

// Start begins the spinner animation.
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
				// \r\033[K clears the line so a shorter message doesn't leave stale characters.
				glyph := SpinnerStyle.Render(string(Frames[i%len(Frames)]))
				fmt.Fprintf(s.w, "\r\033[K%s %s", glyph, m)
				i++
			case <-s.done:
				return
			}
		}
	}()
}

// Stop ends the animation and clears the spinner line.
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
