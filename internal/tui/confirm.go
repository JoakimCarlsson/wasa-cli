package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// confirmDecisionMsg is the typed result a confirmDialog reports up to its
// parent: confirmed is true when the user accepted the prompt, false when they
// cancelled it. The dialog emits it as a command rather than returning a private
// enum, so results flow up through the normal message path.
type confirmDecisionMsg struct{ confirmed bool }

func confirmDecision(confirmed bool) tea.Cmd {
	return func() tea.Msg { return confirmDecisionMsg{confirmed: confirmed} }
}

// confirmDialog is a reusable yes/no modal: a titled, bordered box with a body
// prompt and two focusable buttons rendered like a web dialog. Left/right (or
// tab) move focus between the buttons, enter chooses the focused one, and the
// y/n/esc shortcuts choose directly. It owns only its presentation and focus
// state; the parent decides what confirming means. The cancel button starts
// focused so a stray enter on a destructive prompt cancels rather than confirms.
type confirmDialog struct {
	th          Theme
	title       string
	body        string
	confirmText string
	cancelText  string
	onConfirm   bool // which button is focused; false is the cancel button
	danger      bool // style the title and confirm button as destructive
}

func newConfirmDialog(
	th Theme,
	title, body, confirmText, cancelText string,
	danger bool,
) confirmDialog {
	return confirmDialog{
		th:          th,
		title:       title,
		body:        body,
		confirmText: confirmText,
		cancelText:  cancelText,
		danger:      danger,
	}
}

func (d confirmDialog) update(msg tea.Msg) (confirmDialog, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return d, nil
	}
	switch key.String() {
	case "left", "right", "tab", "shift+tab", "h", "l":
		d.onConfirm = !d.onConfirm
		return d, nil
	case "enter":
		return d, confirmDecision(d.onConfirm)
	case "y":
		return d, confirmDecision(true)
	case "n", "esc", "q":
		return d, confirmDecision(false)
	}
	return d, nil
}

func (d confirmDialog) view() string {
	heading := d.th.titleStyle
	if d.danger {
		heading = d.th.errorStyle.Bold(true)
	}
	top := heading.Render(d.title) + "\n\n" + d.body

	buttons := d.buttons()
	if w := lipgloss.Width(top); lipgloss.Width(buttons) < w {
		buttons = lipgloss.PlaceHorizontal(w, lipgloss.Center, buttons)
	}
	return d.th.modalStyle.Render(top + "\n\n" + buttons)
}

// buttons renders the cancel/confirm pair, filling the focused one and dimming
// the other. The confirm button fills red when the dialog is destructive.
func (d confirmDialog) buttons() string {
	cancelStyle, confirmStyle := d.th.btnInactiveStyle, d.th.btnInactiveStyle
	if d.onConfirm {
		confirmStyle = d.th.btnConfirmStyle
		if d.danger {
			confirmStyle = d.th.btnDangerStyle
		}
	} else {
		cancelStyle = d.th.btnCancelStyle
	}
	return strings.Join([]string{
		cancelStyle.Render(d.cancelText),
		confirmStyle.Render(d.confirmText),
	}, "  ")
}
