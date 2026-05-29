package ui

import (
	"errors"
	"io"

	"github.com/charmbracelet/huh"
)

// Confirm returns true only on Yes; No / esc / ctrl-c collapse to false.
func Confirm(in io.Reader, out io.Writer, message string) (yes bool, err error) {
	var v bool
	field := huh.NewConfirm().
		Title(message).
		Affirmative("Yes").
		Negative("No").
		Value(&v)

	form := huh.NewForm(huh.NewGroup(field)).
		WithTheme(EmailableTheme()).
		WithKeyMap(EscKeyMap()).
		WithInput(in).
		WithOutput(out)

	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return false, nil
		}
		return false, err
	}
	return v, nil
}
