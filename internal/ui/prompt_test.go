package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
)

// newPromptModel mirrors the input Prompt builds, minus the program loop.
func newPromptModel(mask bool) promptModel {
	ti := textinput.New()
	ti.Focus()
	ti.Prompt = ""
	if mask {
		ti.EchoMode = textinput.EchoPassword
		ti.EchoCharacter = '•'
	}
	return promptModel{label: "Key:", input: ti}
}

// typePrompt feeds each key to a fresh model and returns the result.
func typePrompt(mask bool, keys ...string) promptModel {
	m := newPromptModel(mask)
	for _, k := range keys {
		next, _ := m.Update(key(k))
		m = next.(promptModel)
	}
	return m
}

func TestPromptKeyHandling(t *testing.T) {
	cases := []struct {
		name       string
		keys       []string
		wantValue  string
		wantCancel bool
		wantQuit   bool
	}{
		{"types then submits", []string{"s", "k", "_", "x", "enter"}, "sk_x", false, true},
		{"esc cancels", []string{"s", "k", "esc"}, "sk", true, true},
		{"ctrl+c cancels", []string{"ctrl+c"}, "", true, true},
		{"typing without submit", []string{"a", "b"}, "ab", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := typePrompt(false, tc.keys...)
			if got := m.input.Value(); got != tc.wantValue {
				t.Errorf("value = %q, want %q", got, tc.wantValue)
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

// TestPromptViewClearsOnQuit asserts the prompt wipes itself on exit so the
// caller's confirmation isn't drawn under a stale prompt.
func TestPromptViewClearsOnQuit(t *testing.T) {
	m := typePrompt(false, "enter")
	if got := m.View(); got != "" {
		t.Errorf("View after quit = %q, want empty", got)
	}
}

// TestPromptMasksInput asserts masked entry never echoes the typed value,
// rendering bullets instead — the point of mask mode for secrets.
func TestPromptMasksInput(t *testing.T) {
	m := typePrompt(true, "a", "b", "c")
	v := m.View()
	if strings.Contains(v, "abc") {
		t.Errorf("masked view leaked input: %q", v)
	}
	if !strings.Contains(v, "•••") {
		t.Errorf("masked view = %q, want bullets", v)
	}
}
