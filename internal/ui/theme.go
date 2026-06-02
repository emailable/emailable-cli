package ui

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

var dimColor = lipgloss.Color("241")

// EmailableTheme returns the huh form theme using the Emailable brand colors.
func EmailableTheme() *huh.Theme {
	t := huh.ThemeBase()

	t.Group.Title = lipgloss.NewStyle().Bold(true).Foreground(BrandPurple)
	t.Group.Description = lipgloss.NewStyle().Foreground(dimColor)

	t.Focused.Base = t.Focused.Base.BorderForeground(BrandPurpleSoft)
	t.Focused.Title = lipgloss.NewStyle().Bold(true).Foreground(BrandPurple)
	t.Focused.Description = lipgloss.NewStyle().Foreground(dimColor)
	t.Focused.ErrorIndicator = lipgloss.NewStyle().Foreground(lipgloss.Color("#EE6F84")).SetString(" *")
	t.Focused.ErrorMessage = lipgloss.NewStyle().Foreground(lipgloss.Color("#EE6F84"))

	t.Focused.SelectSelector = lipgloss.NewStyle().Bold(true).Foreground(BrandPurple).SetString("❯ ")
	t.Focused.MultiSelectSelector = lipgloss.NewStyle().Bold(true).Foreground(BrandPurple).SetString("❯ ")
	t.Focused.SelectedOption = lipgloss.NewStyle().Bold(true).Foreground(BrandPurple)
	t.Focused.SelectedPrefix = lipgloss.NewStyle().Foreground(BrandPurple).SetString("✓ ")
	t.Focused.UnselectedPrefix = lipgloss.NewStyle().Foreground(dimColor).SetString("• ")

	t.Focused.FocusedButton = t.Focused.FocusedButton.
		Background(BrandPurple).
		Foreground(lipgloss.Color("0")).
		Bold(true)
	t.Focused.BlurredButton = t.Focused.BlurredButton.
		Background(lipgloss.NoColor{}).
		Foreground(dimColor)
	t.Focused.Next = t.Focused.FocusedButton

	t.Focused.TextInput.Cursor = lipgloss.NewStyle().Foreground(BrandPurple)
	t.Focused.TextInput.Placeholder = lipgloss.NewStyle().Foreground(dimColor)
	t.Focused.TextInput.Prompt = lipgloss.NewStyle().Foreground(BrandPurple)
	t.Focused.TextInput.Text = lipgloss.NewStyle()

	t.Blurred = t.Focused
	t.Blurred.Title = lipgloss.NewStyle().Foreground(dimColor)
	t.Blurred.Base = t.Blurred.Base.BorderForeground(dimColor)
	t.Blurred.SelectSelector = lipgloss.NewStyle().Foreground(dimColor).SetString("  ")
	t.Blurred.MultiSelectSelector = lipgloss.NewStyle().Foreground(dimColor).SetString("  ")

	helpStyle := lipgloss.NewStyle().Foreground(dimColor)
	t.Help.ShortKey = helpStyle
	t.Help.ShortDesc = helpStyle
	t.Help.ShortSeparator = helpStyle
	t.Help.FullKey = helpStyle
	t.Help.FullDesc = helpStyle
	t.Help.FullSeparator = helpStyle

	return t
}

// EscKeyMap returns a huh key map that quits on esc or ctrl+c.
func EscKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("esc", "ctrl+c"))
	return km
}
