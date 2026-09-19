package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/joakimcarlsson/wasa-cli/internal/config"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/layout"
)

// helpGroup is one titled block of the help overlay: the actions it lists, in
// the order they are shown. Grouping is by what the user is trying to do, not
// by the order the actions happen to be declared in, so the overlay reads as an
// index of the cockpit rather than a dump of the keymap.
type helpGroup struct {
	title   string
	actions [][2]string
}

// helpGroups is the full keymap as the overlay presents it. Every action in
// config's list-mode bindings appears exactly once, so a binding cannot be
// added without a place to document it.
var helpGroups = []helpGroup{
	{"sessions", [][2]string{
		{config.ActionNew, "create a session"},
		{config.ActionAttach, "attach to the selected session"},
		{config.ActionKill, "kill the selected session"},
		{config.ActionPause, "pause the selected session"},
		{config.ActionResume, "resume the selected session"},
		{config.ActionDelete, "delete the session and its worktree"},
	}},
	{"move", [][2]string{
		{config.ActionCursorUp, "previous session"},
		{config.ActionCursorDown, "next session"},
		{config.ActionTabNext, "next workspace"},
		{config.ActionTabPrev, "previous workspace"},
		{config.ActionPaneTab, "cycle the right pane"},
		{config.ActionFilter, "filter this workspace"},
		{config.ActionGlobalFilter, "jump to any session"},
	}},
	{"workspaces", [][2]string{
		{config.ActionWorkspaceAdd, "add a repository"},
		{config.ActionWorkspaceDelete, "remove this workspace"},
	}},
	{"record", [][2]string{
		{config.ActionRecordToggle, "toggle recording for this repo"},
		{config.ActionCheckpoints, "browse checkpoints"},
		{config.ActionCheckpointSearch, "search checkpoints"},
	}},
	{"cockpit", [][2]string{
		{config.ActionConfig, "settings"},
		{config.ActionHelp, "this help"},
		{config.ActionQuit, "quit"},
	}},
}

// enterHelp opens the help overlay over the session list.
func (m Model) enterHelp() (tea.Model, tea.Cmd) {
	m.mode = modeHelp
	m.status = ""
	return m, nil
}

// updateHelp dismisses the help overlay on any key press: it is a reference
// card, not a mode with its own actions, so there is nothing to get stuck in.
func (m Model) updateHelp(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyPressMsg); !ok {
		return m, nil
	}
	m.mode = modeList
	return m, m.afterListChange()
}

// helpView renders the overlay: every binding under its group heading, the key
// column aligned across the whole card so the eye runs down one edge. Groups
// are packed into as many columns as it takes to fit the terminal's height, so
// the card never runs off the bottom of a short window. It is what the footer's
// five hints defer to, so the footer can stay short.
func (m Model) helpView() string {
	keyW := m.helpKeyWidth()
	blocks := make([]string, 0, len(helpGroups))
	for _, g := range helpGroups {
		var b strings.Builder
		b.WriteString(m.theme.HelpSectionStyle.Render(g.title))
		for _, a := range g.actions {
			b.WriteByte('\n')
			b.WriteString(m.helpRow(a[0], a[1], keyW))
		}
		blocks = append(blocks, b.String())
	}

	title := m.theme.TitleStyle.Render("keys") + m.theme.DimStyle.Render(
		strings.Repeat(" ", layout.Snug)+"any key to close",
	)
	body := m.helpColumns(blocks, m.helpBodyRows())
	return m.theme.PickerStyle.Render(
		title + strings.Repeat("\n", layout.Snug) + body,
	)
}

// helpBodyRows is how many rows the card's groups may occupy: the terminal
// height less the frame the overlay itself draws — its border, padding, title
// and the blank under it — floored so a very short window still gets a column
// rather than none.
func (m Model) helpBodyRows() int {
	frame := m.theme.PickerStyle.GetVerticalFrameSize() + layout.Loose
	return max(m.height-frame, helpMinRows)
}

// helpMinRows is the shortest column the packer will build. Below it the
// overlay would be more column separator than content.
const helpMinRows = 6

// helpColumns packs the group blocks into side-by-side columns, each at most
// rows tall, and joins them with a gutter. Groups are kept whole: a group is
// never split across two columns, because its heading is what makes its
// bindings findable.
func (m Model) helpColumns(blocks []string, rows int) string {
	cols := make([]string, 0, len(blocks))
	cur, curRows := make([]string, 0, len(blocks)), 0
	for _, b := range blocks {
		h := lipgloss.Height(b)
		if len(cur) > 0 && curRows+layout.Snug+h > rows {
			cols = append(
				cols,
				strings.Join(cur, strings.Repeat("\n", layout.Snug)),
			)
			cur, curRows = nil, 0
		}
		if len(cur) > 0 {
			curRows += layout.Snug
		}
		cur = append(cur, b)
		curRows += h
	}
	if len(cur) > 0 {
		cols = append(
			cols,
			strings.Join(cur, strings.Repeat("\n", layout.Snug)),
		)
	}
	if len(cols) == 1 {
		return cols[0]
	}
	gutter := strings.Repeat(" ", layout.Loose)
	spaced := make([]string, 0, len(cols)*2-1)
	for i, c := range cols {
		if i > 0 {
			spaced = append(spaced, gutter)
		}
		spaced = append(spaced, padColumn(c))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, spaced...)
}

// padColumn squares a column off to its widest line, so the next column starts
// at a straight edge rather than following the ragged right of this one.
func padColumn(col string) string {
	lines := strings.Split(col, "\n")
	w := 0
	for _, l := range lines {
		w = max(w, ansi.StringWidth(l))
	}
	for i, l := range lines {
		lines[i] = l + strings.Repeat(" ", w-ansi.StringWidth(l))
	}
	return strings.Join(lines, "\n")
}

// helpRow renders one binding: the key glyph right-aligned in the key column,
// then its description.
func (m Model) helpRow(action, desc string, keyW int) string {
	label := m.menuKey(action)
	pad := max(keyW-ansi.StringWidth(label), 0)
	return strings.Repeat(" ", pad) + m.theme.HelpKeyStyle.Render(label) +
		strings.Repeat(" ", layout.Snug) +
		m.theme.HelpDescStyle.Render(desc)
}

// helpKeyWidth is the width of the overlay's key column: the widest bound key
// across every group, so remapping a binding to a longer chord widens the
// column instead of breaking the alignment.
func (m Model) helpKeyWidth() int {
	w := 0
	for _, g := range helpGroups {
		for _, a := range g.actions {
			w = max(w, ansi.StringWidth(m.menuKey(a[0])))
		}
	}
	return w
}

// helpHint is the footer's pointer at the overlay, always last so the key that
// reveals everything else sits in a fixed place.
func (m Model) helpHint() [2]string {
	return [2]string{m.menuKey(config.ActionHelp), "help"}
}
