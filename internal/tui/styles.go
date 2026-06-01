package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/joakimcarlsson/wasa/internal/config"
)

// The cockpit's lipgloss styles. They are package-level so the view code can
// reference them directly, but their colours are not fixed: applyTheme rebuilds
// every style from a config.Theme. The package initialises them from the
// built-in default theme, and New re-applies the resolved theme at startup, so
// zero config reproduces the historical palette while a config.json recolours the
// whole cockpit. The aesthetic, adapted from claude-squad / agent-deck: a purple
// accent on borders and the active tab, a green status dot for running and a dim
// grey one for exited, and a light selection band that flips the row text dark.
var (
	paneStyle      lipgloss.Style
	paneTitleStyle lipgloss.Style

	activeTabStyle   lipgloss.Style
	inactiveTabStyle lipgloss.Style

	paneTabActiveStyle   lipgloss.Style
	paneTabInactiveStyle lipgloss.Style

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
)

func init() { applyTheme(config.Default().Theme) }

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

// applyTheme rebuilds every package style from t. It is called once at init with
// the default theme and again by New with the resolved theme.
func applyTheme(t config.Theme) {
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

	paneStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent)

	paneTitleStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(accent).
		Padding(0, 1)

	activeTabStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(onAccent).
		Background(accent).
		Padding(0, 2)

	inactiveTabStyle = lipgloss.NewStyle().
		Foreground(desc).
		Padding(0, 2)

	paneTabActiveStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	paneTabInactiveStyle = lipgloss.NewStyle().Foreground(desc)

	runningDotStyle = lipgloss.NewStyle().Foreground(running)
	waitingDotStyle = lipgloss.NewStyle().Foreground(waiting)
	idleDotStyle = lipgloss.NewStyle().Foreground(idle)
	exitedDotStyle = lipgloss.NewStyle().Foreground(exited)

	rowTitleStyle = lipgloss.NewStyle().Foreground(title)
	rowDescStyle = lipgloss.NewStyle().Foreground(desc)

	selRowTitleStyle = lipgloss.NewStyle().
		Bold(true).
		Background(selBg).
		Foreground(selFg)
	selRowDescStyle = lipgloss.NewStyle().
		Background(selBg).
		Foreground(selFg)

	bannerStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	dimStyle = lipgloss.NewStyle().Foreground(desc)
	errorStyle = lipgloss.NewStyle().Foreground(danger)

	modalStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(danger).
		Padding(1, 2)

	pickerStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Padding(1, 2)

	matchStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)

	btnBase := lipgloss.NewStyle().Padding(0, 3)
	btnInactiveStyle = btnBase.
		Foreground(desc).
		Background(inactiveBtnBg)
	btnCancelStyle = btnBase.
		Bold(true).
		Foreground(onAccent).
		Background(accent)
	btnConfirmStyle = btnCancelStyle
	btnDangerStyle = btnBase.
		Bold(true).
		Foreground(onAccent).
		Background(danger)

	menuKeyStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(themeColor(t.MenuKey))
	menuDescStyle = lipgloss.NewStyle().
		Foreground(themeColor(t.MenuDesc))
	menuSepStyle = lipgloss.NewStyle().
		Foreground(themeColor(t.MenuSep))

	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	focusedLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	labelStyle = lipgloss.NewStyle().Foreground(desc)
}

const (
	branchIcon  = "Ꮧ"
	runningIcon = "●"
	waitingIcon = "◆"
	idleIcon    = "○"
	exitedIcon  = "●"
	menuSep     = " • "
)
