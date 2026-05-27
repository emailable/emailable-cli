package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// key builds a KeyMsg for a named key (e.g. "up") or a single rune ("k").
func key(s string) tea.KeyMsg {
	switch s {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// send applies a sequence of keys to a fresh two-choice model and returns the
// resulting model.
func send(keys ...string) selectModel {
	m := selectModel{choices: []Choice{{Label: "a"}, {Label: "b"}}}
	for _, k := range keys {
		next, _ := m.Update(key(k))
		m = next.(selectModel)
	}
	return m
}

func TestSelectNavigation(t *testing.T) {
	cases := []struct {
		name       string
		keys       []string
		wantCursor int
		wantCancel bool
		wantQuit   bool
	}{
		{"down moves to second", []string{"down"}, 1, false, false},
		{"down clamps at end", []string{"down", "down", "down"}, 1, false, false},
		{"up clamps at start", []string{"up"}, 0, false, false},
		{"j/k navigate", []string{"j", "k"}, 0, false, false},
		{"enter selects current", []string{"down", "enter"}, 1, false, true},
		{"esc cancels", []string{"down", "esc"}, 1, true, true},
		{"ctrl+c cancels", []string{"ctrl+c"}, 0, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := send(tc.keys...)
			if m.cursor != tc.wantCursor {
				t.Errorf("cursor = %d, want %d", m.cursor, tc.wantCursor)
			}
			if m.canceled != tc.wantCancel {
				t.Errorf("canceled = %v, want %v", m.canceled, tc.wantCancel)
			}
			if m.quitting != tc.wantQuit {
				t.Errorf("quitting = %v, want %v", m.quitting, tc.wantQuit)
			}
		})
	}
}

// TestSelectViewClearsOnQuit asserts the menu wipes itself on exit so the
// caller's confirmation isn't drawn under a stale menu.
func TestSelectViewClearsOnQuit(t *testing.T) {
	m := send("enter")
	if got := m.View(); got != "" {
		t.Errorf("View after quit = %q, want empty", got)
	}
}
