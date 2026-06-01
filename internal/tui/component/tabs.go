package component

import (
	"github.com/charmbracelet/lipgloss"
)

// Tabs is a row of connected, browser-style tab boxes spanning a fixed width.
// The active tab's bottom border opens into the content window that the caller
// draws beneath the row, while the inactive tabs close against the window's top
// edge — the lipgloss tabs idiom (after claude-squad). It is purely
// presentational: it holds the labels, the active index and the two box styles,
// and Render lays them out. The caller owns the active index and the window
// below.
type Tabs struct {
	Names         []string
	Active        int
	ActiveStyle   lipgloss.Style
	InactiveStyle lipgloss.Style
}

// Render lays the tabs out across width cells, splitting it evenly and giving
// the last tab any remainder, and joins the active/inactive corner glyphs so the
// active tab flows into the window beneath it.
func (t Tabs) Render(width int) string {
	n := len(t.Names)
	if n == 0 {
		return ""
	}
	tabW := width / n
	lastW := width - tabW*(n-1)

	boxes := make([]string, n)
	for i, name := range t.Names {
		w := tabW
		if i == n-1 {
			w = lastW
		}

		active := i == t.Active
		style := t.InactiveStyle
		if active {
			style = t.ActiveStyle
		}
		border, _, _, _, _ := style.GetBorder()
		switch i {
		case 0:
			if active {
				border.BottomLeft = "│"
			} else {
				border.BottomLeft = "├"
			}
		case n - 1:
			if active {
				border.BottomRight = "│"
			} else {
				border.BottomRight = "┤"
			}
		}
		style = style.Border(border)
		boxes[i] = style.Width(w - style.GetHorizontalFrameSize()).Render(name)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, boxes...)
}
