package modal

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/joakimcarlsson/wasa/internal/tui/component"
)

// ConfirmAcceptedMsg is emitted by a ConfirmDialog when the user accepts the
// prompt; the parent runs its stored confirm command in response.
type ConfirmAcceptedMsg struct{}

// ConfirmCancelledMsg is emitted by a ConfirmDialog when the user declines or
// dismisses the prompt; the parent returns to the list with no change.
type ConfirmCancelledMsg struct{}

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

// Update routes a key message into the dialog, returning the updated dialog and
// a command that emits ConfirmAcceptedMsg or ConfirmCancelledMsg on the key that
// settles the choice, or nil otherwise.
func (d ConfirmDialog) Update(msg tea.Msg) (ConfirmDialog, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return d, nil
	}
	switch key.String() {
	case "left", "right", "tab", "shift+tab", "h", "l":
		d.onConfirm = !d.onConfirm
		return d, nil
	case "enter":
		if d.onConfirm {
			return d, accepted
		}
		return d, cancelled
	case "y":
		return d, accepted
	case "n", "esc", "q":
		return d, cancelled
	}
	return d, nil
}

func accepted() tea.Msg { return ConfirmAcceptedMsg{} }

func cancelled() tea.Msg { return ConfirmCancelledMsg{} }

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
