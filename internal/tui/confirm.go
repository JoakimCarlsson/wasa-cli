package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/joakimcarlsson/wasa/internal/tui/component"
)

// confirmResult is what a confirmDialog update reports back to its parent.
type confirmResult int

const (
	confirmNone confirmResult = iota
	confirmYes
	confirmNo
)

// confirmDialog is a reusable yes/no modal: a titled, bordered box with a body
// prompt and two focusable buttons rendered like a web dialog. Left/right (or
// tab) move focus between the buttons, enter chooses the focused one, and the
// y/n/esc shortcuts choose directly. It owns only its presentation and focus
// state; the parent decides what confirming means. The cancel button starts
// focused so a stray enter on a destructive prompt cancels rather than confirms.
type confirmDialog struct {
	theme       component.Theme
	title       string
	body        string
	confirmText string
	cancelText  string
	onConfirm   bool // which button is focused; false is the cancel button
	danger      bool // style the title and confirm button as destructive
}

func newConfirmDialog(
	theme component.Theme,
	title, body, confirmText, cancelText string,
	danger bool,
) confirmDialog {
	return confirmDialog{
		theme:       theme,
		title:       title,
		body:        body,
		confirmText: confirmText,
		cancelText:  cancelText,
		danger:      danger,
	}
}

func (d confirmDialog) update(msg tea.Msg) (confirmDialog, confirmResult) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return d, confirmNone
	}
	switch key.String() {
	case "left", "right", "tab", "shift+tab", "h", "l":
		d.onConfirm = !d.onConfirm
		return d, confirmNone
	case "enter":
		if d.onConfirm {
			return d, confirmYes
		}
		return d, confirmNo
	case "y":
		return d, confirmYes
	case "n", "esc", "q":
		return d, confirmNo
	}
	return d, confirmNone
}

func (d confirmDialog) view() string {
	heading := d.theme.TitleStyle
	if d.danger {
		heading = d.theme.ErrorStyle.Bold(true)
	}
	top := heading.Render(d.title) + "\n\n" + d.body

	buttons := d.buttons()
	if w := lipgloss.Width(top); lipgloss.Width(buttons) < w {
		buttons = lipgloss.PlaceHorizontal(w, lipgloss.Center, buttons)
	}
	return d.theme.ModalStyle.Render(top + "\n\n" + buttons)
}

// buttons renders the cancel/confirm pair, filling the focused one and dimming
// the other. The confirm button fills red when the dialog is destructive.
func (d confirmDialog) buttons() string {
	cancelStyle, confirmStyle := d.theme.BtnInactiveStyle, d.theme.BtnInactiveStyle
	if d.onConfirm {
		confirmStyle = d.theme.BtnConfirmStyle
		if d.danger {
			confirmStyle = d.theme.BtnDangerStyle
		}
	} else {
		cancelStyle = d.theme.BtnCancelStyle
	}
	return strings.Join([]string{
		cancelStyle.Render(d.cancelText),
		confirmStyle.Render(d.confirmText),
	}, "  ")
}
