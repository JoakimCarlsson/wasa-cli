package modal

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/joakimcarlsson/wasa/internal/tui/component"
)

// ConfirmResult is what a ConfirmDialog update reports back to its parent.
type ConfirmResult int

const (
	// ConfirmNone means the dialog has nothing to report this update.
	ConfirmNone ConfirmResult = iota
	// ConfirmYes means the user accepted the prompt.
	ConfirmYes
	// ConfirmNo means the user declined or dismissed the prompt.
	ConfirmNo
)

// ConfirmDialog is a reusable yes/no modal: a titled, bordered box with a body
// prompt and two focusable buttons rendered like a web dialog. Left/right (or
// tab) move focus between the buttons, enter chooses the focused one, and the
// y/n/esc shortcuts choose directly. It owns only its presentation and focus
// state; the parent decides what confirming means. The cancel button starts
// focused so a stray enter on a destructive prompt cancels rather than confirms.
type ConfirmDialog struct {
	theme       component.Theme
	title       string
	body        string
	confirmText string
	cancelText  string
	onConfirm   bool
	danger      bool
}

// NewConfirmDialog builds a yes/no dialog with the given title, prebuilt body,
// button labels and destructive styling, styled with theme. The cancel button
// starts focused.
func NewConfirmDialog(
	theme component.Theme,
	title, body, confirmText, cancelText string,
	danger bool,
) ConfirmDialog {
	return ConfirmDialog{
		theme:       theme,
		title:       title,
		body:        body,
		confirmText: confirmText,
		cancelText:  cancelText,
		danger:      danger,
	}
}

// Update routes a key message into the dialog, reporting the user's choice via
// ConfirmResult.
func (d ConfirmDialog) Update(msg tea.Msg) (ConfirmDialog, ConfirmResult) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return d, ConfirmNone
	}
	switch key.String() {
	case "left", "right", "tab", "shift+tab", "h", "l":
		d.onConfirm = !d.onConfirm
		return d, ConfirmNone
	case "enter":
		if d.onConfirm {
			return d, ConfirmYes
		}
		return d, ConfirmNo
	case "y":
		return d, ConfirmYes
	case "n", "esc", "q":
		return d, ConfirmNo
	}
	return d, ConfirmNone
}

// View renders the dialog box.
func (d ConfirmDialog) View() string {
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
func (d ConfirmDialog) buttons() string {
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
