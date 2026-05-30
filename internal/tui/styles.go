package tui

import "github.com/charmbracelet/lipgloss"

// Palette, adapted from the claude-squad / agent-deck aesthetic: a purple
// accent on borders and the active tab, a green status dot for running and a
// dim grey one for exited, and a light selection band that flips the row text
// dark.
var (
	accent     = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
	runningCol = lipgloss.Color("#51bd73")
	exitedCol  = lipgloss.Color("#888888")
	titleCol   = lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#dddddd"}
	descCol    = lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"}
	selBg      = lipgloss.Color("#dde4f0")
	selFg      = lipgloss.Color("#1a1a1a")
)

var (
	paneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accent)

	paneTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(accent).
			Padding(0, 1)

	activeTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("230")).
			Background(accent).
			Padding(0, 2)

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(descCol).
				Padding(0, 2)

	runningDotStyle = lipgloss.NewStyle().Foreground(runningCol)
	exitedDotStyle  = lipgloss.NewStyle().Foreground(exitedCol)

	rowTitleStyle = lipgloss.NewStyle().Foreground(titleCol)
	rowDescStyle  = lipgloss.NewStyle().Foreground(descCol)

	selRowTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Background(selBg).
				Foreground(selFg)
	selRowDescStyle = lipgloss.NewStyle().
			Background(selBg).
			Foreground(selFg)

	previewStyle = lipgloss.NewStyle().Foreground(titleCol)

	bannerStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	dimStyle    = lipgloss.NewStyle().Foreground(descCol)
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#de613e"))

	menuKeyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#655F5F", Dark: "#cfcaca"})
	menuDescStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#7A7474", Dark: "#9C9494"})
	menuSepStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#DDDADA", Dark: "#3C3C3C"})

	titleStyle        = lipgloss.NewStyle().Bold(true).Foreground(accent)
	focusedLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	labelStyle        = lipgloss.NewStyle().Foreground(descCol)
)

const (
	branchIcon  = "Ꮧ"
	runningIcon = "●"
	exitedIcon  = "●"
	menuSep     = " • "
)
