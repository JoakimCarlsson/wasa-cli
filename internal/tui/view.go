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
		titleS.Render(pad(head, w)),
		descS.Render(pad(sub, w)),
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
	outerW := contentW + 2
	contentH := max(bodyH-(tabRowRows-1), 1)

	n := len(paneTabNames)
	tabW := outerW / n
	lastW := outerW - tabW*(n-1)

	tabs := make([]string, n)
	for i, name := range paneTabNames {
		w := tabW
		if i == n-1 {
			w = lastW
		}

		style := m.theme.PaneTabInactiveStyle
		if paneTab(i) == m.pane {
			style = m.theme.PaneTabActiveStyle
		}
		border, _, _, _, _ := style.GetBorder()
		switch {
		case i == 0 && paneTab(i) == m.pane:
			border.BottomLeft = "│"
		case i == 0:
			border.BottomLeft = "├"
		case i == n-1 && paneTab(i) == m.pane:
			border.BottomRight = "│"
		case i == n-1:
			border.BottomRight = "┤"
		}
		style = style.Border(border)
		tabs[i] = style.Width(w - style.GetHorizontalFrameSize()).Render(name)
	}

	row := lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
	window := m.theme.PaneWindowStyle.Width(contentW).Height(contentH).Render(
		m.paneBody(contentW, contentH),
	)
	return lipgloss.JoinVertical(lipgloss.Left, row, window)
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

// diffBody renders the Diff tab: a colorized git diff of the selected worktree
// session against its recorded base commit, in a scrollable viewport under an
// additions/deletions summary line. A plain (non-worktree) session shows an
// explanatory state rather than an error; a worktree with no changes shows an
// empty state; and the diff is shown only once it has been computed for the
// current selection.
func (m Model) diffBody(w, h int) string {
	s := m.selectedSession()
	if s == nil {
		return m.theme.DimStyle.Render("No session selected.")
	}
	if s.Branch == "" || s.WorktreePath == "" || s.BaseCommit == "" {
		return m.theme.DimStyle.Render(
			"Diff is only available for worktree sessions.",
		)
	}
	if m.diffSID != s.ID {
		return m.theme.DimStyle.Render("Loading diff…")
	}
	if m.diffErr != nil {
		return m.theme.ErrorStyle.Render("diff error: " + m.diffErr.Error())
	}
	if strings.TrimSpace(m.diffText) == "" {
		return m.theme.DimStyle.Render("No changes.")
	}

	vp := m.diffVP
	vp.Width = max(w, 1)
	vp.Height = max(h-1, 1)
	return diffSummaryLine(m.theme, m.diffAdded, m.diffRemoved) +
		"\n" + vp.View()
}

// diffSummaryLine renders the "N additions(+) / M deletions(-)" header above the
// diff, the additions in the add colour and the deletions in the delete colour.
func diffSummaryLine(theme Theme, added, removed int) string {
	return theme.DiffAddStyle.Render(fmt.Sprintf("%d additions(+)", added)) +
		theme.DimStyle.Render(" / ") +
		theme.DiffDelStyle.Render(fmt.Sprintf("%d deletions(-)", removed))
}

// colorizeDiff styles a plain unified diff line by line: hunk headers in the
// accent, additions green and deletions red, and the file/metadata lines dimmed.
// The context lines are left unstyled. git emits the diff without colour, so the
// cockpit colours it itself to match the theme.
func colorizeDiff(theme Theme, text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = styleDiffLine(theme, line)
	}
	return strings.Join(lines, "\n")
}

func styleDiffLine(theme Theme, line string) string {
	switch {
	case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
		return theme.DiffMetaStyle.Render(line)
	case strings.HasPrefix(line, "@@"):
		return theme.DiffHunkStyle.Render(line)
	case strings.HasPrefix(line, "+"):
		return theme.DiffAddStyle.Render(line)
	case strings.HasPrefix(line, "-"):
		return theme.DiffDelStyle.Render(line)
	case strings.HasPrefix(line, "diff "),
		strings.HasPrefix(line, "index "),
		strings.HasPrefix(line, "new file"),
		strings.HasPrefix(line, "deleted file"),
		strings.HasPrefix(line, "rename "),
		strings.HasPrefix(line, "similarity "):
		return theme.DiffMetaStyle.Render(line)
	default:
		return line
	}
}

// terminalBody renders the Terminal tab: a capture of the selected session's
// companion shell. Until the first capture for the current selection arrives it
// shows a starting hint, so a stale capture from a previously selected session
// is never shown as if it were this one's.
func (m Model) terminalBody(w, h int) string {
	s := m.selectedSession()
	if s == nil {
		return m.theme.DimStyle.Render("No session selected.")
	}
	if m.termShown != companionName(s) ||
		strings.TrimSpace(ansi.Strip(m.termContent)) == "" {
		return m.theme.DimStyle.Render("Starting shell…")
	}
	return renderCapture(m.termContent, w, h)
}

func (m Model) previewBody(w, h int) string {
	s := m.selectedSession()
	if s == nil {
		return m.theme.DimStyle.Render("No session selected.")
	}
	if s.Status != registry.StatusRunning {
		return m.theme.DimStyle.Render("Session exited — nothing to preview.")
	}
	// The capture carries the agent's own escape sequences (tmux capture-pane
	// -e), so emptiness must be judged on the visible text, not the raw bytes.
	if strings.TrimSpace(ansi.Strip(m.preview)) == "" {
		return m.theme.DimStyle.Render("Waiting for output…")
	}
	return renderCapture(m.preview, w, h)
}

// renderCapture fits a tmux pane capture to a w×h area for the Preview and
// Terminal tabs: it expands tabs, keeps the last h lines so the freshest output
// shows, and truncates each line to the visible width without slicing an escape
// sequence — resetting at the end so an unterminated colour cannot bleed into
// the pane border or the padding lipgloss adds. The capture is already styled,
// so it is emitted as-is and never re-styled.
func renderCapture(content string, w, h int) string {
	lines := strings.Split(strings.ReplaceAll(content, "\t", "    "), "\n")
	if len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	for i, ln := range lines {
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
	return keyLabel(m.keys.primary(action))
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
func confirmBody(theme Theme, prompt string, s *registry.Session) string {
	_, ref := sessionLabel(s)
	return prompt + "\n\n" + theme.DimStyle.Render(
		fmt.Sprintf("%s %s · %s", branchIcon, ref, s.ProfileName),
	)
}

func statusDot(theme Theme, s sessionstatus.Status) string {
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

func noWorkspaceBanner(theme Theme) string {
	return theme.BannerStyle.Render("No workspaces yet.") + "\n\n" +
		theme.DimStyle.Render(
			"Press n to start a plain session here.\n\n"+
				"Or add a repo with\nwasa workspace add <path>\n"+
				"or run wasa inside a git repo.",
		)
}

func noSessionBanner(theme Theme, name string) string {
	title := "No sessions here."
	if name != "" {
		title = fmt.Sprintf("No sessions in %s.", name)
	}
	return theme.BannerStyle.Render(title) + "\n\n" +
		theme.DimStyle.Render("Press n to create one.")
}
