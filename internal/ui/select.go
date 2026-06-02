package ui

import (
	"errors"
	"io"
	"strings"

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

	// Pad labels to a common width so the dimmed hints line up in a column.
	maxLabel := 0
	for _, c := range choices {
		if c.Hint != "" {
			if w := lipgloss.Width(c.Label); w > maxLabel {
				maxLabel = w
			}
		}
	}

	opts := make([]huh.Option[int], len(choices))
	for i, c := range choices {
		label := c.Label
		if c.Hint != "" {
			pad := maxLabel - lipgloss.Width(c.Label)
			label = label + strings.Repeat(" ", pad+2) + dim.Render(c.Hint)
		}
		opts[i] = huh.NewOption(label, i)
	}

	var selected int
	field := huh.NewSelect[int]().
		Title(prompt).
		Options(opts...).
		Filtering(false).
		Value(&selected)

	// Disable the "/" filter binding so the option list is never searchable.
	km := EscKeyMap()
	km.Select.Filter.SetEnabled(false)

	form := huh.NewForm(huh.NewGroup(field)).
		WithTheme(EmailableTheme()).
		WithKeyMap(km).
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
