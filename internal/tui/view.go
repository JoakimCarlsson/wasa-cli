package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/joakimcarlsson/wasa/internal/config"
	"github.com/joakimcarlsson/wasa/internal/registry"
	"github.com/joakimcarlsson/wasa/internal/sessionstatus"
)

// chromeRows is the number of rows the tab bar, menu and status line take from
// the body height. Unlike the column sizing it is not user-configurable: it
// tracks the fixed frame the cockpit draws, not a preference.
const chromeRows = 6

// View implements tea.Model.
func (m Model) View() string {
	if m.mode == modeCreate {
		return m.form.view() + "\n" + m.statusLine()
	}

	if m.mode == modePick || m.mode == modePickBranch {
		bg := lipgloss.Place(
			max(m.width, m.cfg.Layout.CompactWidth), max(m.height-1, 1),
			lipgloss.Left, lipgloss.Top, m.form.view(),
		)
		overlay := m.picker.view()
		if m.mode == modePickBranch {
			overlay = m.branch.view()
		}
		return placeOverlay(overlay, bg) + "\n" + m.statusLine()
	}

	base := m.listView()
	if m.mode == modeConfirm {
		return placeOverlay(m.confirm.view(), base)
	}
	if m.mode == modeConfig {
		return placeOverlay(m.editor.view(), base)
	}
	return base
}

// listView is the cockpit's normal frame: the workspace tabs, the session list
// and preview, the menu and the status line. It is also the background a modal
// floats over, so it is built independently of which mode is active.
func (m Model) listView() string {
	if m.width < m.cfg.Layout.CompactWidth ||
		m.height < m.cfg.Layout.CompactHeight {
		return m.compactView()
	}

	tabs := m.tabBar()

	bodyH := max(m.height-chromeRows, 3)
	listW := m.listColWidth()
	previewW := m.width - listW - 4

	list := paneStyle.Width(listW).Height(bodyH).Render(
		m.paneTitle("sessions") + "\n" + m.sessionList(listW),
	)
	right := paneStyle.Width(previewW).Height(bodyH).Render(
		m.paneTabStrip() + "\n" + m.paneBody(previewW, bodyH-1),
	)
	body := lipgloss.JoinHorizontal(lipgloss.Top, list, right)

	return lipgloss.JoinVertical(
		lipgloss.Left,
		tabs,
		body,
		m.menuBar(),
		m.statusLine(),
	)
}

// listColWidth is the width of the session-list column: the configured fraction
// of the terminal width, floored at the configured minimum so the list stays
// usable on a narrow terminal.
func (m Model) listColWidth() int {
	return max(
		int(float64(m.width)*m.cfg.Layout.ListColFrac),
		m.cfg.Layout.MinListWidth,
	)
}

func (m Model) paneTitle(name string) string {
	return paneTitleStyle.Render(name)
}

func (m Model) tabBar() string {
	if len(m.workspaces) == 0 {
		return inactiveTabStyle.Render("no workspaces")
	}
	active := m.tabIndex()
	parts := make([]string, len(m.workspaces))
	for i, w := range m.workspaces {
		if i == active {
			parts[i] = activeTabStyle.Render(w.Name)
		} else {
			parts[i] = inactiveTabStyle.Render(w.Name)
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Bottom, parts...)
}

func (m Model) sessionList(paneW int) string {
	ss := m.sessions()
	if len(ss) == 0 {
		if len(m.workspaces) == 0 {
			return noWorkspaceBanner()
		}
		ws := m.currentWorkspace()
		name := ""
		if ws != nil {
			name = ws.Name
		}
		return noSessionBanner(name)
	}

	inner := paneW - 2
	var b strings.Builder
	for i, s := range ss {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(m.sessionRow(i, s, inner))
		b.WriteString("\n")
	}
	return b.String()
}

func (m Model) sessionRow(i int, s *registry.Session, w int) string {
	selected := i == m.cursor
	titleS, descS := rowTitleStyle, rowDescStyle
	if selected {
		titleS, descS = selRowTitleStyle, selRowDescStyle
	}

	title, ref := sessionLabel(s)
	rs := m.runtimeStatus(s)
	prefix := fmt.Sprintf(" %d ", i+1)
	head := fmt.Sprintf("%s%s %s", prefix, statusDot(rs), title)
	sub := fmt.Sprintf(
		"   %s %s · %s · %s", branchIcon, ref, s.ProfileName, rs.Label(),
	)

	return lipgloss.JoinVertical(
		lipgloss.Left,
		titleS.Render(pad(head, w)),
		descS.Render(pad(sub, w)),
	)
}

// paneTabStrip renders the right pane's tab labels — Preview, Diff, Terminal —
// with the active one accented and the rest dimmed. It stands where the single
// "preview" pane title used to, one line tall, so the body height accounting is
// unchanged.
func (m Model) paneTabStrip() string {
	parts := make([]string, len(paneTabNames))
	for i, name := range paneTabNames {
		if paneTab(i) == m.pane {
			parts[i] = paneTabActiveStyle.Render(name)
		} else {
			parts[i] = paneTabInactiveStyle.Render(name)
		}
	}
	sep := menuSepStyle.Render(menuSep)
	return " " + strings.Join(parts, sep)
}

// paneBody renders the body of the active right-pane tab into a w×h area.
func (m Model) paneBody(w, h int) string {
	switch m.pane {
	case paneDiff:
		return m.diffBody(w, h)
	case paneTerminal:
		return m.terminalBody(w, h)
	default:
		return m.previewBody(w, h)
	}
}

// diffBody renders the Diff tab. The diff itself arrives in a later phase; for
// now it is a placeholder so the tab is navigable.
func (m Model) diffBody(_, _ int) string {
	return dimStyle.Render("Diff — not yet implemented.")
}

// terminalBody renders the Terminal tab. The companion shell arrives in a later
// phase; for now it is a placeholder so the tab is navigable.
func (m Model) terminalBody(_, _ int) string {
	return dimStyle.Render("Terminal — not yet implemented.")
}

func (m Model) previewBody(w, h int) string {
	s := m.selectedSession()
	if s == nil {
		return dimStyle.Render("No session selected.")
	}
	if s.Status != registry.StatusRunning {
		return dimStyle.Render("Session exited — nothing to preview.")
	}
	// The capture carries the agent's own escape sequences (tmux capture-pane
	// -e), so emptiness must be judged on the visible text, not the raw bytes.
	if strings.TrimSpace(ansi.Strip(m.preview)) == "" {
		return dimStyle.Render("Waiting for output…")
	}

	lines := strings.Split(strings.ReplaceAll(m.preview, "\t", "    "), "\n")
	if len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	for i, ln := range lines {
		// Truncate by visible width without cutting escape sequences, then
		// reset so an unterminated color can't bleed into the pane border or
		// the spaces lipgloss pads the line with. The captured content is
		// already styled, so it is emitted as-is and never re-styled.
		lines[i] = ansi.Truncate(ln, w, "") + "\x1b[0m"
	}
	return strings.Join(lines, "\n")
}

func (m Model) menuBar() string {
	items := [][2]string{
		{m.menuKey(config.ActionNew), "new"},
		{m.menuKey(config.ActionAttach), "attach"},
		{m.menuKey(config.ActionKill), "kill"},
		{m.menuKey(config.ActionDelete), "delete"},
		{m.menuKey(config.ActionTabNext), "tabs"},
		{m.menuKey(config.ActionPaneTab), "panes"},
		{
			m.menuKey(
				config.ActionCursorUp,
			) + m.menuKey(
				config.ActionCursorDown,
			),
			"select",
		},
		{m.menuKey(config.ActionConfig), "config"},
		{m.menuKey(config.ActionQuit), "quit"},
	}
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = menuKeyStyle.Render(
			it[0],
		) + " " + menuDescStyle.Render(
			it[1],
		)
	}
	return " " + strings.Join(parts, menuSepStyle.Render(menuSep))
}

// menuKey is the glyph the menu bar shows for an action: the effective primary
// binding, so a remapped key is reflected in the hint.
func (m Model) menuKey(action string) string {
	return keyLabel(m.keys.primary(action))
}

func (m Model) statusLine() string {
	if m.err != nil {
		return errorStyle.Render(" error: " + m.err.Error())
	}
	if m.status != "" {
		return dimStyle.Render(" " + m.status)
	}
	return ""
}

func (m Model) compactView() string {
	parts := []string{
		m.tabBar(),
		"",
		m.sessionList(max(m.width, m.cfg.Layout.CompactWidth)),
		m.menuBar(),
	}
	if s := m.statusLine(); s != "" {
		parts = append(parts, s)
	}
	return strings.Join(parts, "\n")
}

// sessionLabel returns a session's display title and its ref (branch, or the
// base of its working directory for a plain session). The title falls back to
// the ref when unset. It is the one place the list and the confirm modals agree
// on how to name a session.
func sessionLabel(s *registry.Session) (title, ref string) {
	ref = s.Branch
	if ref == "" {
		ref = filepath.Base(s.WorkingDir)
	}
	title = s.Title
	if title == "" {
		title = ref
	}
	return title, ref
}

// confirmBody composes a confirm-modal body: the prompt followed by the dimmed
// branch · profile line that identifies the target session.
func confirmBody(prompt string, s *registry.Session) string {
	_, ref := sessionLabel(s)
	return prompt + "\n\n" + dimStyle.Render(
		fmt.Sprintf("%s %s · %s", branchIcon, ref, s.ProfileName),
	)
}

func statusDot(s sessionstatus.Status) string {
	switch s {
	case sessionstatus.Waiting:
		return waitingDotStyle.Render(waitingIcon)
	case sessionstatus.Idle:
		return idleDotStyle.Render(idleIcon)
	case sessionstatus.Exited:
		return exitedDotStyle.Render(exitedIcon)
	default:
		return runningDotStyle.Render(runningIcon)
	}
}

func pad(s string, w int) string {
	if w <= 0 {
		return s
	}
	s = runewidth.Truncate(s, w, "…")
	if gap := w - runewidth.StringWidth(s); gap > 0 {
		s += strings.Repeat(" ", gap)
	}
	return s
}

func noWorkspaceBanner() string {
	return bannerStyle.Render("No workspaces yet.") + "\n\n" +
		dimStyle.Render(
			"Press n to start a plain session here.\n\n"+
				"Or add a repo with\nwasa workspace add <path>\n"+
				"or run wasa inside a git repo.",
		)
}

func noSessionBanner(name string) string {
	title := "No sessions here."
	if name != "" {
		title = fmt.Sprintf("No sessions in %s.", name)
	}
	return bannerStyle.Render(title) + "\n\n" +
		dimStyle.Render("Press n to create one.")
}
