package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/joakimcarlsson/wasa/internal/config"
	"github.com/joakimcarlsson/wasa/internal/registry"
	"github.com/joakimcarlsson/wasa/internal/sessionstatus"
	"github.com/joakimcarlsson/wasa/internal/tui/component"
	"github.com/joakimcarlsson/wasa/internal/tui/pane"
)

// chromeRows is the number of rows the tab bar, menu and status line take from
// the body height. Unlike the column sizing it is not user-configurable: it
// tracks the fixed frame the cockpit draws, not a preference.
const chromeRows = 6

// View implements tea.Model.
func (m Model) View() string {
	if m.mode == modeCreate {
		return m.form.View() + "\n" + m.statusLine()
	}

	if m.mode == modePick || m.mode == modePickBranch {
		bg := lipgloss.Place(
			max(m.width, m.cfg.Layout.CompactWidth), max(m.height-1, 1),
			lipgloss.Left, lipgloss.Top, m.form.View(),
		)
		overlay := m.picker.View()
		if m.mode == modePickBranch {
			overlay = m.branch.View()
		}
		return component.PlaceOverlay(overlay, bg) + "\n" + m.statusLine()
	}

	base := m.listView()
	if m.mode == modeConfirm {
		return component.PlaceOverlay(m.confirm.View(), base)
	}
	if m.mode == modeConfig {
		return component.PlaceOverlay(m.editor.View(), base)
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

	list := m.theme.PaneStyle.Width(listW).Height(bodyH).Render(
		m.paneTitle("sessions") + "\n" + m.sessionList(listW),
	)
	right := m.tabbedRightPane(previewW, bodyH)
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
	return m.theme.PaneTitleStyle.Render(name)
}

func (m Model) tabBar() string {
	if len(m.workspaces) == 0 {
		return m.theme.InactiveTabStyle.Render("no workspaces")
	}
	active := m.tabIndex()
	parts := make([]string, len(m.workspaces))
	for i, w := range m.workspaces {
		if i == active {
			parts[i] = m.theme.ActiveTabStyle.Render(w.Name)
		} else {
			parts[i] = m.theme.InactiveTabStyle.Render(w.Name)
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Bottom, parts...)
}

func (m Model) sessionList(paneW int) string {
	ss := m.sessions()
	if len(ss) == 0 {
		if len(m.workspaces) == 0 {
			return noWorkspaceBanner(m.theme)
		}
		ws := m.currentWorkspace()
		name := ""
		if ws != nil {
			name = ws.Name
		}
		return noSessionBanner(m.theme, name)
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
	titleS, descS := m.theme.RowTitleStyle, m.theme.RowDescStyle
	if selected {
		titleS, descS = m.theme.SelRowTitleStyle, m.theme.SelRowDescStyle
	}

	title, ref := sessionLabel(s)
	rs := m.runtimeStatus(s)
	prefix := fmt.Sprintf(" %d ", i+1)
	head := fmt.Sprintf("%s%s %s", prefix, statusDot(m.theme, rs), title)
	sub := fmt.Sprintf(
		"   %s %s · %s · %s", branchIcon, ref, s.ProfileName, rs.Label(),
	)

	return lipgloss.JoinVertical(
		lipgloss.Left,
		titleS.Render(component.Pad(head, w)),
		descS.Render(component.Pad(sub, w)),
	)
}

// tabRowRows is the height the tab row occupies above the content window: the
// box top border, the label line and the bottom edge that doubles as the
// window's top border.
const tabRowRows = 3

// tabbedRightPane renders the right pane as a row of connected tab boxes —
// Preview, Diff, Terminal — sitting on a content window, in the lipgloss tabs
// idiom (after claude-squad): the tabs span the pane width, the active tab's
// bottom border opens into the window beneath it, and the inactive tabs close
// against the window's top edge. contentW and bodyH are the content width and
// the full body height the pane must fill so it lines up with the sessions
// pane.
func (m Model) tabbedRightPane(contentW, bodyH int) string {
	contentH := max(bodyH-(tabRowRows-1), 1)

	row := component.TabStrip(m.theme, paneTabNames[:], int(m.pane), contentW+2)
	window := m.theme.PaneWindowStyle.Width(contentW).Height(contentH).Render(
		m.paneBody(contentW, contentH),
	)
	return lipgloss.JoinVertical(lipgloss.Left, row, window)
}

// paneBody renders the body of the active right-pane tab into a w×h area. The
// no-session and exited gating that depends on the registry stays here; the
// owning pane machine renders the rest of each tab's states.
func (m Model) paneBody(w, h int) string {
	s := m.selectedSession()
	switch m.pane {
	case paneDiff:
		return m.diff.Body(m.theme, m.diffSession(s), w, h)
	case paneTerminal:
		return m.term.Body(m.theme, m.termSession(s), w, h)
	default:
		if s == nil {
			return m.theme.DimStyle.Render("No session selected.")
		}
		return m.preview.Body(
			m.theme, s.Status == registry.StatusRunning, w, h,
		)
	}
}

// diffSession projects the selected session into the minimal facts the Diff
// pane's body needs to choose its render state.
func (m Model) diffSession(s *registry.Session) pane.DiffSession {
	if s == nil {
		return pane.DiffSession{}
	}
	return pane.DiffSession{
		Selected:     true,
		ID:           s.ID,
		Branch:       s.Branch,
		WorktreePath: s.WorktreePath,
		BaseCommit:   s.BaseCommit,
	}
}

// termSession projects the selected session into the minimal facts the Terminal
// pane's body needs to choose its render state.
func (m Model) termSession(s *registry.Session) pane.TermSession {
	if s == nil {
		return pane.TermSession{}
	}
	return pane.TermSession{
		Selected:      true,
		CompanionName: companionName(s.TmuxName),
	}
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
		parts[i] = m.theme.MenuKeyStyle.Render(
			it[0],
		) + " " + m.theme.MenuDescStyle.Render(
			it[1],
		)
	}
	return " " + strings.Join(parts, m.theme.MenuSepStyle.Render(menuSep))
}

// menuKey is the glyph the menu bar shows for an action: the effective primary
// binding, so a remapped key is reflected in the hint.
func (m Model) menuKey(action string) string {
	return component.KeyLabel(m.keys.Primary(action))
}

func (m Model) statusLine() string {
	if m.err != nil {
		return m.theme.ErrorStyle.Render(" error: " + m.err.Error())
	}
	if m.status != "" {
		return m.theme.DimStyle.Render(" " + m.status)
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
func confirmBody(theme component.Theme, prompt string, s *registry.Session) string {
	_, ref := sessionLabel(s)
	return prompt + "\n\n" + theme.DimStyle.Render(
		fmt.Sprintf("%s %s · %s", branchIcon, ref, s.ProfileName),
	)
}

func statusDot(theme component.Theme, s sessionstatus.Status) string {
	switch s {
	case sessionstatus.Waiting:
		return theme.WaitingDotStyle.Render(waitingIcon)
	case sessionstatus.Idle:
		return theme.IdleDotStyle.Render(idleIcon)
	case sessionstatus.Exited:
		return theme.ExitedDotStyle.Render(exitedIcon)
	default:
		return theme.RunningDotStyle.Render(runningIcon)
	}
}

func noWorkspaceBanner(theme component.Theme) string {
	return theme.BannerStyle.Render("No workspaces yet.") + "\n\n" +
		theme.DimStyle.Render(
			"Press n to start a plain session here.\n\n"+
				"Or add a repo with\nwasa workspace add <path>\n"+
				"or run wasa inside a git repo.",
		)
}

func noSessionBanner(theme component.Theme, name string) string {
	title := "No sessions here."
	if name != "" {
		title = fmt.Sprintf("No sessions in %s.", name)
	}
	return theme.BannerStyle.Render(title) + "\n\n" +
		theme.DimStyle.Render("Press n to create one.")
}
