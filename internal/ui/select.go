package ui

import (
	"errors"
	"io"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// Choice is a selectable option with an optional dimmed hint.
type Choice struct {
	Label string
	Hint  string
}

// Select prompts the user to pick one of choices; ok is false on esc/ctrl-c.
func Select(in io.Reader, out io.Writer, prompt string, choices []Choice) (idx int, ok bool, err error) {
	if len(choices) == 0 {
		return 0, false, errors.New("ui.Select: no choices")
	}
	dim := lipgloss.NewStyle().Foreground(dimColor)
	opts := make([]huh.Option[int], len(choices))
	for i, c := range choices {
		label := c.Label
		if c.Hint != "" {
			label = label + "  " + dim.Render(c.Hint)
		}
		opts[i] = huh.NewOption(label, i)
	}

	var selected int
	field := huh.NewSelect[int]().
		Title(prompt).
		Options(opts...).
		Value(&selected)

	form := huh.NewForm(huh.NewGroup(field)).
		WithTheme(EmailableTheme()).
		WithKeyMap(EscKeyMap()).
		WithInput(in).
		WithOutput(out)

	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return selected, true, nil
}
