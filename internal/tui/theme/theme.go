package theme

import (
	"charm.land/lipgloss/v2"

	"github.com/joakimcarlsson/wasa-cli/internal/config"
)

// Theme is the cockpit's resolved set of lipgloss styles. Its colours are not
// fixed: NewTheme rebuilds every style from a config.Theme, so the zero-config
// cockpit reproduces the historical palette while a config.json recolours the
// whole cockpit. The aesthetic, adapted from claude-squad / agent-deck: a purple
// accent on borders and the active tab, a green status dot for running and a dim
// grey one for exited, and a light selection band that flips the row text dark.
//
// Every style is derived from Tokens, the semantic colour layer, rather than
// from a config field directly — so two widgets that mean the same thing get
// the same colour by construction. Tokens is kept on the Theme so a caller that
// must compose a colour itself (a syntax highlighter, a one-off badge) draws
// from the same vocabulary instead of reaching back into the config.
//
// The fields are exported so the root view code and the bespoke editors that
// hold a Theme can read them across the package boundary.
type Theme struct {
	Tokens Tokens

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

	// RowNumStyle is the row's ordinal gutter and RowMetaStyle its trailing
	// metadata column — the two weights that give a list row its hierarchy
	// without a blank line between rows.
	RowNumStyle     lipgloss.Style
	RowMetaStyle    lipgloss.Style
	SelRowNumStyle  lipgloss.Style
	SelRowMetaStyle lipgloss.Style

	// RuleStyle is the faint horizontal rule that separates sections inside a
	// pane, and BadgeStyle the small accent-on-nothing label beside a title.
	RuleStyle  lipgloss.Style
	BadgeStyle lipgloss.Style

	// StatusStyle is the right-aligned end of the footer, where the cockpit's
	// own counters sit opposite the key hints.
	StatusStyle lipgloss.Style

	// HelpKeyStyle and HelpDescStyle are the help overlay's two columns, and
	// HelpSectionStyle its group headings.
	HelpKeyStyle     lipgloss.Style
	HelpDescStyle    lipgloss.Style
	HelpSectionStyle lipgloss.Style
}

// NewTheme builds the cockpit's styles from t. New calls it with the resolved
// theme at startup and applyConfig calls it again when the config changes live.
func NewTheme(t config.Theme) Theme {
	tok := NewTokens(t)

	accent := tok.Accent
	running := tok.Success
	waiting := tok.Warning
	idle := tok.Info
	exited := tok.Neutral
	title := tok.Text
	desc := tok.Muted
	selFg := tok.SelectionFg
	selBg := tok.SelectionBg
	danger := tok.Danger
	onAccent := tok.OnAccent
	inactiveBtnBg := tok.Inactive

	var th Theme
	th.Tokens = tok

	th.PaneStyle = lipgloss.NewStyle()

	th.PaneTitleStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(accent)

	th.ActiveTabStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(onAccent).
		Background(accent).
		Padding(0, 1)

	th.InactiveTabStyle = lipgloss.NewStyle().
		Foreground(desc).
		Padding(0, 1)

	th.PaneTabInactiveStyle = lipgloss.NewStyle().
		Foreground(desc).
		Padding(0, 1)
	th.PaneTabActiveStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(accent).
		Underline(true).
		Padding(0, 1)
	th.PaneWindowStyle = lipgloss.NewStyle()

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

	addBg := tok.AddBg
	delBg := tok.DelBg
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

	th.StatusStyle = lipgloss.NewStyle().Foreground(tok.Faint)

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
		Foreground(tok.KeyHint)
	th.MenuDescStyle = lipgloss.NewStyle().
		Foreground(tok.KeyDesc)
	th.MenuSepStyle = lipgloss.NewStyle().
		Foreground(tok.Faint)

	th.TitleStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	th.FocusedLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	th.LabelStyle = lipgloss.NewStyle().Foreground(desc)

	th.RowNumStyle = lipgloss.NewStyle().Foreground(tok.Faint)
	th.RowMetaStyle = lipgloss.NewStyle().Foreground(desc)
	th.SelRowNumStyle = lipgloss.NewStyle().
		Background(selBg).
		Foreground(selFg)
	th.SelRowMetaStyle = th.SelRowNumStyle

	th.RuleStyle = lipgloss.NewStyle().Foreground(tok.Faint)
	th.BadgeStyle = lipgloss.NewStyle().
		Foreground(accent).
		Bold(true)

	th.HelpKeyStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	th.HelpDescStyle = lipgloss.NewStyle().Foreground(title)
	th.HelpSectionStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(desc)

	return th
}
