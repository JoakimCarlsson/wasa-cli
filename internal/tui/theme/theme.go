package theme

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/joakimcarlsson/wasa-cli/internal/config"
)

// Theme is the cockpit's resolved set of lipgloss styles. Its colours are not
// fixed: NewTheme rebuilds every style from a config.Theme, so the zero-config
// cockpit reproduces the historical palette while a config.json recolours the
// whole cockpit. The aesthetic, adapted from claude-squad / agent-deck: a purple
// accent on borders and the active tab, a green status dot for running and a dim
// grey one for exited, and a light selection band that flips the row text dark.
//
// The fields are exported so the root view code and the bespoke editors that
// hold a Theme can read them across the package boundary.
type Theme struct {
	PaneStyle      lipgloss.Style
	PaneTitleStyle lipgloss.Style

	ActiveTabStyle   lipgloss.Style
	InactiveTabStyle lipgloss.Style

	PaneTabActiveStyle   lipgloss.Style
	PaneTabInactiveStyle lipgloss.Style
	PaneWindowStyle      lipgloss.Style

	RunningDotStyle lipgloss.Style
	WaitingDotStyle lipgloss.Style
	IdleDotStyle    lipgloss.Style
	ExitedDotStyle  lipgloss.Style

	RowTitleStyle lipgloss.Style
	RowDescStyle  lipgloss.Style

	SelRowTitleStyle lipgloss.Style
	SelRowDescStyle  lipgloss.Style

	BannerStyle lipgloss.Style
	DimStyle    lipgloss.Style
	ErrorStyle  lipgloss.Style

	DiffAddStyle  lipgloss.Style
	DiffDelStyle  lipgloss.Style
	DiffHunkStyle lipgloss.Style
	DiffMetaStyle lipgloss.Style

	DiffAddLineStyle lipgloss.Style
	DiffDelLineStyle lipgloss.Style
	DiffGutterStyle  lipgloss.Style
	DiffFileStyle    lipgloss.Style

	ModalStyle  lipgloss.Style
	PickerStyle lipgloss.Style
	MatchStyle  lipgloss.Style

	BtnInactiveStyle lipgloss.Style
	BtnCancelStyle   lipgloss.Style
	BtnConfirmStyle  lipgloss.Style
	BtnDangerStyle   lipgloss.Style

	MenuKeyStyle  lipgloss.Style
	MenuDescStyle lipgloss.Style
	MenuSepStyle  lipgloss.Style

	TitleStyle        lipgloss.Style
	FocusedLabelStyle lipgloss.Style
	LabelStyle        lipgloss.Style
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

// NewTheme builds the cockpit's styles from t. New calls it with the resolved
// theme at startup and applyConfig calls it again when the config changes live.
func NewTheme(t config.Theme) Theme {
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

	th.PaneStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent)

	th.PaneTitleStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(accent).
		Padding(0, 1)

	th.ActiveTabStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(onAccent).
		Background(accent).
		Padding(0, 2)

	th.InactiveTabStyle = lipgloss.NewStyle().
		Foreground(desc).
		Padding(0, 2)

	th.PaneTabInactiveStyle = lipgloss.NewStyle().
		Border(tabBorderWithBottom("┴", "─", "┴"), true).
		BorderForeground(accent).
		Foreground(desc).
		Align(lipgloss.Center)
	th.PaneTabActiveStyle = lipgloss.NewStyle().
		Border(tabBorderWithBottom("┘", " ", "└"), true).
		BorderForeground(accent).
		Bold(true).
		Foreground(accent).
		Align(lipgloss.Center)
	th.PaneWindowStyle = lipgloss.NewStyle().
		BorderForeground(accent).
		Border(lipgloss.RoundedBorder(), false, true, true, true)

	th.RunningDotStyle = lipgloss.NewStyle().Foreground(running)
	th.WaitingDotStyle = lipgloss.NewStyle().Foreground(waiting)
	th.IdleDotStyle = lipgloss.NewStyle().Foreground(idle)
	th.ExitedDotStyle = lipgloss.NewStyle().Foreground(exited)

	th.RowTitleStyle = lipgloss.NewStyle().Foreground(title)
	th.RowDescStyle = lipgloss.NewStyle().Foreground(desc)

	th.SelRowTitleStyle = lipgloss.NewStyle().
		Bold(true).
		Background(selBg).
		Foreground(selFg)
	th.SelRowDescStyle = lipgloss.NewStyle().
		Background(selBg).
		Foreground(selFg)

	th.BannerStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	th.DimStyle = lipgloss.NewStyle().Foreground(desc)
	th.ErrorStyle = lipgloss.NewStyle().Foreground(danger)

	th.DiffAddStyle = lipgloss.NewStyle().Foreground(running)
	th.DiffDelStyle = lipgloss.NewStyle().Foreground(danger)
	th.DiffHunkStyle = lipgloss.NewStyle().Foreground(accent)
	th.DiffMetaStyle = lipgloss.NewStyle().Foreground(desc)

	addBg := themeColor(t.DiffAddBg)
	delBg := themeColor(t.DiffDelBg)
	th.DiffAddLineStyle = lipgloss.NewStyle().
		Foreground(title).
		Background(addBg)
	th.DiffDelLineStyle = lipgloss.NewStyle().
		Foreground(title).
		Background(delBg)
	th.DiffGutterStyle = lipgloss.NewStyle().Foreground(desc)
	th.DiffFileStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)

	th.ModalStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(danger).
		Padding(1, 2)

	th.PickerStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Padding(1, 2)

	th.MatchStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)

	btnBase := lipgloss.NewStyle().Padding(0, 3)
	th.BtnInactiveStyle = btnBase.
		Foreground(desc).
		Background(inactiveBtnBg)
	th.BtnCancelStyle = btnBase.
		Bold(true).
		Foreground(onAccent).
		Background(accent)
	th.BtnConfirmStyle = th.BtnCancelStyle
	th.BtnDangerStyle = btnBase.
		Bold(true).
		Foreground(onAccent).
		Background(danger)

	th.MenuKeyStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(themeColor(t.MenuKey))
	th.MenuDescStyle = lipgloss.NewStyle().
		Foreground(themeColor(t.MenuDesc))
	th.MenuSepStyle = lipgloss.NewStyle().
		Foreground(themeColor(t.MenuSep))

	th.TitleStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	th.FocusedLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	th.LabelStyle = lipgloss.NewStyle().Foreground(desc)

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
