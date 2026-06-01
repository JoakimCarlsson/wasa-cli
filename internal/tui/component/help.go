package component

import (
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
)

// Help is a single-line key/help bar: it renders a row of "key desc" hints
// separated by a bullet, built on bubbles/help so the layout and ellipsis
// handling are the library's rather than hand-rolled. The caller supplies the
// key, description and separator styles and, on each View, the bindings to show
// — so a remapped key reflects in the hint without Help holding any config.
type Help struct {
	model help.Model
}

// NewHelp builds a help bar styled with the given key, description and separator
// styles and the bullet separator used between hints.
func NewHelp(keyStyle, descStyle, sepStyle lipgloss.Style, separator string) Help {
	m := help.New()
	m.ShortSeparator = separator
	m.Styles.ShortKey = keyStyle
	m.Styles.ShortDesc = descStyle
	m.Styles.ShortSeparator = sepStyle
	return Help{model: m}
}

// View renders the bindings as a single-line short-help row.
func (h Help) View(bindings []key.Binding) string {
	return h.model.ShortHelpView(bindings)
}
