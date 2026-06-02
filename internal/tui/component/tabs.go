package component

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/joakimcarlsson/wasa/internal/tui/theme"
)

// TabStrip renders a row of connected tab boxes spanning totalWidth, in the
// lipgloss tabs idiom (after claude-squad): the active tab's bottom border opens
// into the content window that sits beneath the row, while the inactive tabs
// close against the window's top edge, and the first and last tabs square off
// their outer bottom corners so the row meets the window's side rules. The
// labels are drawn in active-tab order; active is the index of the open tab.
func TabStrip(t theme.Theme, labels []string, active, totalWidth int) string {
	n := len(labels)
	tabW := totalWidth / n
	lastW := totalWidth - tabW*(n-1)

	tabs := make([]string, n)
	for i, name := range labels {
		w := tabW
		if i == n-1 {
			w = lastW
		}

		style := t.PaneTabInactiveStyle
		if i == active {
			style = t.PaneTabActiveStyle
		}
		border, _, _, _, _ := style.GetBorder()
		switch i {
		case 0:
			border.BottomLeft = squareCorner(active == 0, "│", "├")
		case n - 1:
			border.BottomRight = squareCorner(active == n-1, "│", "┤")
		}
		style = style.Border(border)
		tabs[i] = style.Width(w - style.GetHorizontalFrameSize()).Render(name)
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
}

// squareCorner picks the bottom-corner glyph for the strip's outer edge: open
// (the active tab's side flowing into the window) when active, otherwise the tee
// that closes an inactive tab against the window's side rule.
func squareCorner(active bool, open, closed string) string {
	if active {
		return open
	}
	return closed
}
