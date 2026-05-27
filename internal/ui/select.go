package ui

import (
	"errors"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Choice is one selectable item in a Select prompt. Hint is optional dimmed
// text shown after the label.
type Choice struct {
	Label string
	Hint  string
}

// Select renders an arrow-key menu of choices on out, reading keystrokes from
// in, and returns the index of the choice the user picked. ok is false when the
// user canceled (esc / ctrl-c) without choosing. The caller must only invoke
// this when stdin and out are both terminals (do not gate on ui.IsTTY, which
// also reports false under NO_COLOR — a NO_COLOR terminal is still interactive).
//
// The menu clears itself on exit, leaving nothing behind, so the caller can
// print its own confirmation of what was chosen.
func Select(in io.Reader, out io.Writer, prompt string, choices []Choice) (idx int, ok bool, err error) {
	if len(choices) == 0 {
		return 0, false, errors.New("ui.Select: no choices")
	}
	m := selectModel{prompt: prompt, choices: choices}
	p := tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(out))
	res, err := p.Run()
	if err != nil {
		return 0, false, err
	}
	final, ok := res.(selectModel)
	if !ok {
		return 0, false, errors.New("ui.Select: unexpected model type")
	}
	if final.canceled {
		return 0, false, nil
	}
	return final.cursor, true, nil
}

// selectModel is the bubbletea model backing Select. Cursor movement clamps at
// the ends rather than wrapping.
type selectModel struct {
	prompt   string
	choices  []Choice
	cursor   int
	canceled bool
	quitting bool
}

func (m selectModel) Init() tea.Cmd { return nil }

func (m selectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.choices)-1 {
			m.cursor++
		}
	case "enter":
		m.quitting = true
		return m, tea.Quit
	case "ctrl+c", "esc", "q":
		m.canceled = true
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

func (m selectModel) View() string {
	if m.quitting {
		return "" // wipe the menu region; the caller prints its own confirmation
	}

	var b strings.Builder
	if m.prompt != "" {
		b.WriteString(selectPromptStyle.Render(m.prompt))
		b.WriteString("\n\n")
	}
	for i, c := range m.choices {
		cursor := "  "
		label := c.Label
		if i == m.cursor {
			cursor = selectCursorStyle.Render("❯ ")
			label = selectActiveStyle.Render(c.Label)
		}
		b.WriteString(cursor)
		b.WriteString(label)
		if c.Hint != "" {
			b.WriteString("  ")
			b.WriteString(selectHintStyle.Render(c.Hint))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(selectHintStyle.Render("↑/↓ or j/k to move · enter to select · esc or q to cancel"))
	return b.String()
}

var (
	selectPromptStyle = lipgloss.NewStyle().Bold(true).Foreground(BrandPurple)
	selectCursorStyle = lipgloss.NewStyle().Bold(true).Foreground(BrandPurple)
	selectActiveStyle = lipgloss.NewStyle().Bold(true).Foreground(BrandPurple)
	selectHintStyle   = lipgloss.NewStyle().Faint(true)
)
