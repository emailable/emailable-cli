package ui

import (
	"errors"
	"io"
	"strings"

	"github.com/charmbracelet/huh"
)

// Prompt returns value, ok, err; ok is false on esc/ctrl-c.
func Prompt(in io.Reader, out io.Writer, label string, mask bool) (value string, ok bool, err error) {
	return PromptWithPlaceholder(in, out, label, "", mask)
}

// PromptWithPlaceholder is Prompt with a placeholder shown in the empty input.
func PromptWithPlaceholder(in io.Reader, out io.Writer, label, placeholder string, mask bool) (value string, ok bool, err error) {
	var v string
	field := huh.NewInput().
		Title(label).
		Placeholder(placeholder).
		Value(&v)
	if mask {
		field = field.EchoMode(huh.EchoModePassword)
	}

	form := huh.NewForm(huh.NewGroup(field)).
		WithTheme(EmailableTheme()).
		WithKeyMap(EscKeyMap()).
		WithInput(in).
		WithOutput(out)

	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return "", false, nil
		}
		return "", false, err
	}
	return strings.TrimSpace(v), true, nil
}
