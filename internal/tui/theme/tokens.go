package theme

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"

	"github.com/joakimcarlsson/wasa-cli/internal/config"
)

// Tokens is the cockpit's semantic colour layer: the handful of roles every
// style is expressed in, resolved once from the config palette. Styles are
// built from a role — Danger, Muted, Success — rather than from a config field,
// so a widget that needs "the destructive colour" cannot end up with a
// different one from the widget beside it, and a new widget has a vocabulary to
// pick from instead of a colour to invent.
//
// The mapping from config is deliberately many-to-one: the config palette names
// elements (running, exited, menuKey) because that is what a user recolours,
// while the styles below name roles.
type Tokens struct {
	// Accent is the cockpit's primary colour — borders, the active tab, focused
	// labels — and OnAccent is the text laid over an accent fill.
	Accent   color.Color
	OnAccent color.Color

	// Text is body copy; Muted is everything subordinate to it (details,
	// gutters, hints). Faint is the quietest rule-and-separator grey.
	Text  color.Color
	Muted color.Color
	Faint color.Color

	// The status roles, in escalating order of demand on attention.
	Success color.Color
	Info    color.Color
	Warning color.Color
	Danger  color.Color
	Neutral color.Color

	// SelectionFg and SelectionBg are the highlighted-row band; Inactive is the
	// fill of a control that is present but not focused.
	SelectionFg color.Color
	SelectionBg color.Color
	Inactive    color.Color

	// AddBg and DelBg are the diff bands the add and delete colours sit on.
	AddBg color.Color
	DelBg color.Color

	// KeyHint and KeyDesc colour the footer's key glyphs and their labels.
	KeyHint color.Color
	KeyDesc color.Color

	// Syntax is the Chroma style name source is colourised with.
	Syntax string
}

// NewTokens resolves a config palette into the semantic roles the styles are
// built from.
func NewTokens(t config.Theme) Tokens {
	return Tokens{
		Accent:   themeColor(t.Accent),
		OnAccent: themeColor(t.OnAccent),

		Text:  themeColor(t.Title),
		Muted: themeColor(t.Desc),
		Faint: themeColor(t.MenuSep),

		Success: themeColor(t.Running),
		Info:    themeColor(t.Idle),
		Warning: themeColor(t.Waiting),
		Danger:  themeColor(t.Danger),
		Neutral: themeColor(t.Exited),

		SelectionFg: themeColor(t.SelectionFg),
		SelectionBg: themeColor(t.SelectionBg),
		Inactive:    themeColor(t.InactiveBtnBg),

		AddBg: themeColor(t.DiffAddBg),
		DelBg: themeColor(t.DiffDelBg),

		KeyHint: themeColor(t.MenuKey),
		KeyDesc: themeColor(t.MenuDesc),

		Syntax: t.Syntax,
	}
}

// themeColor converts a config.Color to a lipgloss colour. A colour whose light
// and dark variants are equal becomes a plain lipgloss.Color (identical to a
// fixed colour); one with distinct variants becomes a compat.AdaptiveColor that
// lipgloss resolves against the terminal background.
func themeColor(c config.Color) color.Color {
	if c.Light == c.Dark {
		return lipgloss.Color(c.Light)
	}
	return compat.AdaptiveColor{
		Light: lipgloss.Color(c.Light),
		Dark:  lipgloss.Color(c.Dark),
	}
}
