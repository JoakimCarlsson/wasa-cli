package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/joakimcarlsson/wasa/internal/config"
)

// Theme is the cockpit's resolved palette: every lipgloss style the view code
// draws with, built once from a config.Theme and passed by value into the
// render paths. It replaces the old package-level style vars so a Model carries
// its own theme, nothing mutates global state, and a test can build a Theme
// without disturbing any other. The aesthetic, adapted from claude-squad /
// agent-deck: a purple accent on borders and the active tab, a green status dot
// for running and a dim grey one for exited, and a light selection band that
// flips the row text dark.
type Theme struct {
	paneStyle      lipgloss.Style
	paneTitleStyle lipgloss.Style

	activeTabStyle   lipgloss.Style
	inactiveTabStyle lipgloss.Style

	paneTabActiveStyle   lipgloss.Style
	paneTabInactiveStyle lipgloss.Style
	paneWindowStyle      lipgloss.Style

	runningDotStyle lipgloss.Style
	waitingDotStyle lipgloss.Style
	idleDotStyle    lipgloss.Style
	exitedDotStyle  lipgloss.Style

	rowTitleStyle lipgloss.Style
	rowDescStyle  lipgloss.Style

	selRowTitleStyle lipgloss.Style
	selRowDescStyle  lipgloss.Style

	bannerStyle lipgloss.Style
	dimStyle    lipgloss.Style
	errorStyle  lipgloss.Style

	diffAddStyle  lipgloss.Style
	diffDelStyle  lipgloss.Style
	diffHunkStyle lipgloss.Style
	diffMetaStyle lipgloss.Style

	modalStyle  lipgloss.Style
	pickerStyle lipgloss.Style
	matchStyle  lipgloss.Style

	btnInactiveStyle lipgloss.Style
	btnCancelStyle   lipgloss.Style
	btnConfirmStyle  lipgloss.Style
	btnDangerStyle   lipgloss.Style

	menuKeyStyle  lipgloss.Style
	menuDescStyle lipgloss.Style
	menuSepStyle  lipgloss.Style

	titleStyle        lipgloss.Style
	focusedLabelStyle lipgloss.Style
	labelStyle        lipgloss.Style
}

// themeColor converts a config.Color to a lipgloss colour. A colour whose light
// and dark variants are equal becomes a plain lipgloss.Color (identical to a
// fixed colour); one with distinct variants becomes an AdaptiveColor that lipgloss
// resolves against the terminal background.
func themeColor(c config.Color) lipgloss.TerminalColor {
	if c.Light == c.Dark {
		return lipgloss.Color(c.Light)
	}
	return lipgloss.AdaptiveColor{Light: c.Light, Dark: c.Dark}
}

// newTheme builds the cockpit's styles from t. Zero config (config.Default's
// theme) reproduces the historical palette; a config.json recolours the whole
// cockpit by recolouring the styles a Model holds, with no global state.
func newTheme(t config.Theme) Theme {
	accent := themeColor(t.Accent)
	running := themeColor(t.Running)
	waiting := themeColor(t.Waiting)
	idle := themeColor(t.Idle)
	exited := themeColor(t.Exited)
	title := themeColor(t.Title)
	desc := themeColor(t.Desc)
	selFg := themeColor(t.SelectionFg)
	selBg := themeColor(t.SelectionBg)
	danger := themeColor(t.Danger)
	onAccent := themeColor(t.OnAccent)
	inactiveBtnBg := themeColor(t.InactiveBtnBg)

	var th Theme

	th.paneStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent)

	th.paneTitleStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(accent).
		Padding(0, 1)

	th.activeTabStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(onAccent).
		Background(accent).
		Padding(0, 2)

	th.inactiveTabStyle = lipgloss.NewStyle().
		Foreground(desc).
		Padding(0, 2)

	th.paneTabInactiveStyle = lipgloss.NewStyle().
		Border(tabBorderWithBottom("┴", "─", "┴"), true).
		BorderForeground(accent).
		Foreground(desc).
		Align(lipgloss.Center)
	th.paneTabActiveStyle = lipgloss.NewStyle().
		Border(tabBorderWithBottom("┘", " ", "└"), true).
		BorderForeground(accent).
		Bold(true).
		Foreground(accent).
		Align(lipgloss.Center)
	th.paneWindowStyle = lipgloss.NewStyle().
		BorderForeground(accent).
		Border(lipgloss.RoundedBorder(), false, true, true, true)

	th.runningDotStyle = lipgloss.NewStyle().Foreground(running)
	th.waitingDotStyle = lipgloss.NewStyle().Foreground(waiting)
	th.idleDotStyle = lipgloss.NewStyle().Foreground(idle)
	th.exitedDotStyle = lipgloss.NewStyle().Foreground(exited)

	th.rowTitleStyle = lipgloss.NewStyle().Foreground(title)
	th.rowDescStyle = lipgloss.NewStyle().Foreground(desc)

	th.selRowTitleStyle = lipgloss.NewStyle().
		Bold(true).
		Background(selBg).
		Foreground(selFg)
	th.selRowDescStyle = lipgloss.NewStyle().
		Background(selBg).
		Foreground(selFg)

	th.bannerStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	th.dimStyle = lipgloss.NewStyle().Foreground(desc)
	th.errorStyle = lipgloss.NewStyle().Foreground(danger)

	th.diffAddStyle = lipgloss.NewStyle().Foreground(running)
	th.diffDelStyle = lipgloss.NewStyle().Foreground(danger)
	th.diffHunkStyle = lipgloss.NewStyle().Foreground(accent)
	th.diffMetaStyle = lipgloss.NewStyle().Foreground(desc)

	th.modalStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(danger).
		Padding(1, 2)

	th.pickerStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Padding(1, 2)

	th.matchStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)

	btnBase := lipgloss.NewStyle().Padding(0, 3)
	th.btnInactiveStyle = btnBase.
		Foreground(desc).
		Background(inactiveBtnBg)
	th.btnCancelStyle = btnBase.
		Bold(true).
		Foreground(onAccent).
		Background(accent)
	th.btnConfirmStyle = th.btnCancelStyle
	th.btnDangerStyle = btnBase.
		Bold(true).
		Foreground(onAccent).
		Background(danger)

	th.menuKeyStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(themeColor(t.MenuKey))
	th.menuDescStyle = lipgloss.NewStyle().
		Foreground(themeColor(t.MenuDesc))
	th.menuSepStyle = lipgloss.NewStyle().
		Foreground(themeColor(t.MenuSep))

	th.titleStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	th.focusedLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	th.labelStyle = lipgloss.NewStyle().Foreground(desc)

	return th
}

// tabBorderWithBottom is a rounded border with its bottom edge overridden, used
// for the pane tab boxes: an inactive tab closes its bottom against the window
// rule with ┴ corners, while the active tab opens its bottom (blank middle, ┘ └
// corners) so the box flows into the content window beneath it, the way a
// browser tab connects to its page.
func tabBorderWithBottom(left, middle, right string) lipgloss.Border {
	b := lipgloss.RoundedBorder()
	b.BottomLeft = left
	b.Bottom = middle
	b.BottomRight = right
	return b
}

const (
	branchIcon  = "Ꮧ"
	runningIcon = "●"
	waitingIcon = "◆"
	idleIcon    = "○"
	exitedIcon  = "●"
	menuSep     = " • "
)
