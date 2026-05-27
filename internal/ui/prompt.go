package ui

import (
	"errors"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// Prompt renders a single-line text input on out, reading keystrokes from in,
// and returns what the user typed. When mask is true the entry is echoed as
// bullets rather than shown verbatim — use it for secrets like API keys, where
// the user still wants visible feedback that their paste landed. ok is false
// when the user canceled (esc / ctrl-c) without submitting.
//
// Like Select, the prompt clears itself on exit so the caller can print its own
// confirmation, and must only be invoked when stdin and out are both terminals
// (do not gate on ui.IsTTY, which also reports false under NO_COLOR — a NO_COLOR
// terminal is still interactive).
func Prompt(in io.Reader, out io.Writer, label string, mask bool) (value string, ok bool, err error) {
	ti := textinput.New()
	ti.Focus()
	ti.Prompt = ""
	if mask {
		ti.EchoMode = textinput.EchoPassword
		ti.EchoCharacter = '•'
	}

	m := promptModel{label: label, input: ti}
	p := tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(out))
	res, err := p.Run()
	if err != nil {
		return "", false, err
	}
	final, ok := res.(promptModel)
	if !ok {
		return "", false, errors.New("ui.Prompt: unexpected model type")
	}
	if final.canceled {
		return "", false, nil
	}
	return strings.TrimSpace(final.input.Value()), true, nil
}

// promptModel is the bubbletea model backing Prompt.
type promptModel struct {
	label    string
	input    textinput.Model
	canceled bool
	quitting bool
}

func (m promptModel) Init() tea.Cmd { return textinput.Blink }

func (m promptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.Type {
		case tea.KeyEnter:
			m.quitting = true
			return m, tea.Quit
		case tea.KeyCtrlC, tea.KeyEsc:
			m.canceled = true
			m.quitting = true
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m promptModel) View() string {
	if m.quitting {
		return "" // wipe the prompt region; the caller prints its own confirmation
	}
	return selectPromptStyle.Render(m.label) + " " + m.input.View()
}
