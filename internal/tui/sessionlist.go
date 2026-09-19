package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/joakimcarlsson/wasa-cli/internal/config"
	"github.com/joakimcarlsson/wasa-cli/internal/registry"
	"github.com/joakimcarlsson/wasa-cli/internal/sessionstatus"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/component"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/layout"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/pane"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/theme"
	"github.com/joakimcarlsson/wasa-cli/internal/worktree"
)

// sessionRowLines is the height of one session row as sessionRows lays it out:
// a title line and a detail line, with no blank between them — rows are told
// apart by the weight and alignment inside them, not by the space around them,
// so twice as many fit on screen. It is what turns an available row budget into
// a count of sessions the list pane can show at once.
const sessionRowLines = 2

// rowGutter is the width of a row's ordinal column, including the space after
// it. The detail line is indented to it so the branch sits under the title
// rather than under the number.
const rowGutter = 4

// View implements tea.Model.
func (m Model) View() tea.View {
	var content string
	switch m.mode {
	case modeCreate:
		content = m.form.View() + "\n" + m.statusLine()
	case modeCheckpoints:
		content = m.checkpointsView()
	case modePick, modePickBranch:
		bg := lipgloss.Place(
			max(m.width, m.cfg.Layout.CompactWidth), max(m.height-1, 1),
			lipgloss.Left, lipgloss.Top, m.form.View(),
		)
		overlay := m.picker.View()
		if m.mode == modePickBranch {
			overlay = m.branch.View()
		}
		content = component.PlaceOverlay(overlay, bg) + "\n" + m.statusLine()
	case modePickWorkspace:
		content = component.Modal(m.picker.View(), m.listView())
	case modeConfirm:
		content = component.Modal(m.confirm.View(), m.listView())
	case modeConfig:
		content = component.Modal(m.editor.View(), m.listView())
	case modeCheckpointSearch:
		content = component.Modal(m.checkpointSearchView(), m.listView())
	case modeGlobalFilter:
		content = component.Modal(m.globalFilterView(), m.listView())
	case modeHelp:
		content = component.Modal(m.helpView(), m.listView())
	default:
		content = m.listView()
	}
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// listView is the cockpit's normal frame: the workspace tabs, the session list
// and preview, the menu and the status line. It is also the background a modal
// floats over, so it is built independently of which mode is active.
func (m Model) listView() string {
	f := m.frame()
	if f.Compact {
		return m.compactView()
	}

	rows := max(f.Body-layout.PaneTabRows, 1)
	list := m.theme.PaneStyle.Width(f.List).Height(f.Body).Render(
		m.paneHeader("sessions", m.listPosition(rows), f.List) + "\n" +
			m.sessionList(f.List, rows),
	)
	right := m.tabbedRightPane(f.Right, f.Body)
	body := lipgloss.JoinHorizontal(
		lipgloss.Top, list, m.columnGutter(f.Body), right,
	)

	return lipgloss.JoinVertical(
		lipgloss.Left,
		m.tabBar(),
		"",
		body,
		m.footer(),
		m.statusLine(),
	)
}

// columnGutter is the divider between the two body panes: a faint vertical
// rule with a space either side, running the height of the body. On the row
// the two panes rule their headers off, it carries the horizontal rule across
// instead, so the three lines meet rather than leaving a gap in the middle of
// the frame.
func (m Model) columnGutter(h int) string {
	lines := make([]string, h)
	for i := range lines {
		if i == headerRuleRow {
			lines[i] = m.theme.RuleStyle.Render(
				strings.Repeat("─", layout.PaneGutter),
			)
			continue
		}
		lines[i] = " " + m.theme.RuleStyle.Render("│") + " "
	}
	return strings.Join(lines, "\n")
}

// headerRuleRow is the body row both panes draw their header rule on — the
// line under the pane title and the tab strip. The divider matches it there.
const headerRuleRow = 1

// footer is the hint bar: the contextual key hints on the left and the
// cockpit's own counters flush right, the way a shell prompt line carries its
// status. Both sides are drawn on one row so the frame costs a single line.
func (m Model) footer() string {
	left := m.menuBar()
	right := m.footerStatus()
	gap := m.width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return component.PadAnsi(left, m.width)
	}
	return left + strings.Repeat(" ", gap) + right
}

// footerStatus is the footer's right-hand end: how many sessions the active
// workspace holds and whether its repository is being recorded. It is where
// the recording state lives now that the sessions pane has no title bar to
// hang a badge off.
func (m Model) footerStatus() string {
	if len(m.tabList()) == 0 {
		return ""
	}
	parts := []string{fmt.Sprintf("%d sessions", len(m.sessions()))}
	if m.currentWorkspace() != nil {
		if agents := m.recording[m.activeID]; len(agents) > 0 {
			parts = append(parts, "rec "+strings.Join(agents, ", "))
		} else {
			parts = append(parts, "rec off")
		}
	}
	return m.theme.StatusStyle.Render(strings.Join(parts, " · ") + " ")
}

// frame is the cockpit's resolved geometry for the current terminal size: the
// body height and the two column widths every view sizes against. One frame
// keeps the session list, the checkpoints browser and the right pane aligned
// with each other instead of each re-deriving the arithmetic.
func (m Model) frame() layout.Frame {
	return layout.New(m.cfg.Layout, m.width, m.height)
}

// listColWidth is the width of the session-list column: the configured fraction
// of the terminal width, floored at the configured minimum so the list stays
// usable on a narrow terminal.
func (m Model) listColWidth() int {
	return m.frame().List
}

func (m Model) paneTitle(name string) string {
	return m.theme.PaneTitleStyle.Render(name)
}

// paneHeader is a body column's heading: the pane's name with an optional
// trailing badge, over a faint rule spanning the column. It is the left
// column's counterpart to the right pane's tab strip, so both columns start
// their content on the same row and share one horizontal baseline — the thing
// that holds a borderless two-column frame together.
func (m Model) paneHeader(name, badge string, w int) string {
	head := m.paneTitle(name) + badge
	if pad := w - ansi.StringWidth(head); pad > 0 {
		head += strings.Repeat(" ", pad)
	}
	return head + "\n" + m.theme.RuleStyle.Render(
		strings.Repeat("─", max(w, 0)),
	)
}

func (m Model) tabBar() string {
	tabs := m.tabList()
	if len(tabs) == 0 {
		return m.theme.InactiveTabStyle.Render("no workspaces")
	}
	active := m.tabIndex()
	parts := make([]string, len(tabs))
	for i, t := range tabs {
		label := t.name
		if len(m.recording[t.id]) > 0 {
			label += " " + recordIcon
		}
		if i == active {
			parts[i] = m.theme.ActiveTabStyle.Render(label)
		} else {
			parts[i] = m.theme.InactiveTabStyle.Render(label)
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Bottom, parts...)
}

// sessionList renders the list pane's body within rows terminal lines, so a
// list longer than the pane scrolls with the cursor instead of running off the
// bottom of the screen.
func (m Model) sessionList(paneW, rows int) string {
	if len(m.tabList()) == 0 {
		return noWorkspaceBanner(m.theme, m.menuKey(config.ActionWorkspaceAdd))
	}

	ss := m.sessions()
	if m.filter.active {
		return m.filter.input.View() + "\n\n" +
			m.filterBody(ss, paneW, rows-2)
	}
	if len(ss) == 0 {
		if m.activeID == "" {
			return orphanEmptyBanner(m.theme)
		}
		name := ""
		if ws := m.currentWorkspace(); ws != nil {
			name = ws.Name
		}
		return noSessionBanner(m.theme, name)
	}
	return m.sessionRows(ss, paneW, rows)
}

// filterBody is the list body while filtering: the matched rows, or a clear
// "no matches" line when the query narrows the list to nothing — so the pane
// reads as deliberately empty rather than blank.
func (m Model) filterBody(
	ss []*registry.Session, paneW, rows int,
) string {
	if len(ss) == 0 {
		return m.theme.DimStyle.Render("  no matches")
	}
	return m.sessionRows(ss, paneW, rows)
}

// sessionRows renders the session list body within rows terminal lines: each
// session as a two-line row, numbered from one in the order shown. Only the
// window around the cursor is drawn, so a list taller than the pane scrolls
// instead of running off the bottom of the screen; the numbering still counts
// from the top of the whole list, so a row's number is its position in the
// list rather than in the window.
func (m Model) sessionRows(
	ss []*registry.Session, paneW, rows int,
) string {
	inner := paneW - 2
	start, end := listWindow(len(ss), m.cursor, visibleSessions(rows))
	var b strings.Builder
	for i := start; i < end; i++ {
		b.WriteString(m.sessionRow(i, ss[i], inner))
		b.WriteString("\n")
	}
	return b.String()
}

// visibleSessions is how many session rows fit in rows terminal lines, at
// least one so the cursor's row is drawn however short the pane is.
func visibleSessions(rows int) int {
	return max(rows/sessionRowLines, 1)
}

// listWindow returns the half-open range of a list of n items to draw when
// capacity of them fit and the cursor sits on the given item. The window keeps
// the cursor as near the middle as the ends allow: it stays at the top until
// the cursor passes the middle, then follows it a row at a time, and stops
// against the last item — so the cursor is always drawn and the list never
// scrolls past its end.
func listWindow(n, cursor, capacity int) (start, end int) {
	if n <= capacity {
		return 0, n
	}
	start = min(max(cursor-capacity/2, 0), n-capacity)
	return start, start + capacity
}

// listPosition is the dim "6/23" counter shown beside the sessions title once
// the list is taller than the pane, so a scrolled list says where in it the
// cursor sits. It is empty whenever the whole list is on screen.
func (m Model) listPosition(rows int) string {
	if m.filter.active {
		rows -= 2
	}
	ss := m.sessions()
	if len(ss) <= visibleSessions(rows) || m.cursor >= len(ss) {
		return ""
	}
	return m.theme.DimStyle.Render(
		fmt.Sprintf("  %d/%d", m.cursor+1, len(ss)),
	)
}

// sessionRow renders one session as two lines with no blank between them: the
// title line carries the ordinal, the status dot and the title against the
// row's churn and record tokens flush right, and the detail line carries the
// branch and profile, indented to sit under the title, against the status
// label flush right. Hierarchy comes from that alignment and from the weight
// difference between the two lines, so rows stay distinct while packing twice
// as many onto the pane as a blank-separated list did.
func (m Model) sessionRow(i int, s *registry.Session, w int) string {
	selected := i == m.cursor
	titleS, descS := m.theme.RowTitleStyle, m.theme.RowDescStyle
	numS := m.theme.RowNumStyle
	if selected {
		titleS, descS = m.theme.SelRowTitleStyle, m.theme.SelRowDescStyle
		numS = m.theme.SelRowNumStyle
	}

	title, ref := sessionLabel(s)
	title, ref = m.highlightMatch(title, ref, selected)
	rs := m.runtimeStatus(s)

	head := numS.Render(fmt.Sprintf("%*d ", rowGutter-1, i+1)) +
		m.rowDot(rs, selected) + titleS.Render(" "+title)
	if len(m.collisions[s.ID]) > 0 {
		head += " " + m.collisionBadge(selected)
	}

	detail := descS.Render(
		strings.Repeat(" ", rowGutter) + branchIcon + " " + ref +
			" · " + s.ProfileName,
	)

	return lipgloss.JoinVertical(
		lipgloss.Left,
		rowLine(titleS, head, m.rowTokens(s, selected), w),
		rowLine(descS, detail, descS.Render(rs.Label()+" "), w),
	)
}

// rowLine composes one line of a list row: left content, right content flush
// against the column edge, and fill-styled spaces between them so a selection
// band runs unbroken across the whole width. The left side is truncated when
// the two would collide, because the right side is the shorter, denser fact.
func rowLine(fill lipgloss.Style, left, right string, w int) string {
	if w <= 0 {
		return left + right
	}
	lw, rw := ansi.StringWidth(left), ansi.StringWidth(right)
	if lw+rw > w {
		if rw >= w {
			return component.PadAnsi(left, w)
		}
		left = ansi.Truncate(left, w-rw, "…")
		lw = ansi.StringWidth(left)
	}
	return left + fill.Render(strings.Repeat(" ", max(w-lw-rw, 0))) + right
}

// rowTokens is the title line's right-hand column: the churn and record tokens
// with a trailing space off the pane edge, or "" when the session has neither.
func (m Model) rowTokens(s *registry.Session, selected bool) string {
	churn := m.churnToken(s, selected)
	rec := m.recordedToken(s, selected)
	style := m.theme.RowMetaStyle
	if selected {
		style = m.theme.SelRowMetaStyle
	}
	switch {
	case churn == "" && rec == "":
		return ""
	case churn == "":
		return rec + style.Render(" ")
	case rec == "":
		return churn + style.Render(" ")
	}
	return churn + style.Render(" ") + rec + style.Render(" ")
}

// rowDot is the status dot as it appears in a row: on the selected row it takes
// the selection band's background so the glyph sits on the band rather than
// punching a hole in it.
func (m Model) rowDot(rs sessionstatus.Status, selected bool) string {
	st := dotStyle(m.theme, rs)
	if selected {
		st = st.Background(m.theme.SelRowTitleStyle.GetBackground())
	}
	return st.Render(dotIcon(rs))
}

// collisionBadge is the warning glyph marking a row whose session shares
// changed paths with another, on the selection band where one applies.
func (m Model) collisionBadge(selected bool) string {
	warn := m.theme.ErrorStyle
	if selected {
		warn = warn.Background(m.theme.SelRowTitleStyle.GetBackground())
	}
	return warn.Render(collisionIcon)
}

// churnToken renders a worktree session's +N/−M churn in the diff add/remove
// colours, or "" when there is nothing to show: a plain session, a session whose
// churn has not been computed yet, or a clean worktree (zero churn renders no
// +0/−0 noise). On the selected row the add/remove styles inherit the selection
// band's background so the coloured digits sit on the band rather than punching a
// hole in it.
func (m Model) churnToken(s *registry.Session, selected bool) string {
	if s.Branch == "" || s.WorktreePath == "" || s.BaseCommit == "" {
		return ""
	}
	c, ok := m.churn[s.ID]
	if !ok || (c.added == 0 && c.removed == 0) {
		return ""
	}
	add, del := m.theme.DiffAddStyle, m.theme.DiffDelStyle
	if selected {
		bg := m.theme.SelRowDescStyle.GetBackground()
		add, del = add.Background(bg), del.Background(bg)
	}
	return add.Render(fmt.Sprintf("+%d", c.added)) + "/" +
		del.Render(fmt.Sprintf("−%d", c.removed))
}

// recordedToken renders "⏺ N" for a session that produced a checkpoint, where N
// is the commit count of the session's newest checkpoint — the same value the
// `wasa checkpoints` COMMITS column shows. It returns "" when the session has no
// checkpoint (recording off, or none written), so absence is the signal and a
// not-recorded row is never marked and never shows an error state. On the
// selected row the styles inherit the selection band's background so the token
// sits on the band rather than punching a hole in it, mirroring churnToken.
func (m Model) recordedToken(s *registry.Session, selected bool) string {
	e, ok := m.recorded[s.ID]
	if !ok {
		return ""
	}
	icon, dim := m.theme.RunningDotStyle, m.theme.DimStyle
	if selected {
		bg := m.theme.SelRowDescStyle.GetBackground()
		icon, dim = icon.Background(bg), dim.Background(bg)
	}
	return icon.Render(recordIcon) +
		dim.Render(fmt.Sprintf(" %d", len(e.Meta.Commits)))
}

// highlightMatch lights up the fuzzy-matched characters of a row's title and ref
// while filtering, mirroring the pickers. It is a no-op when not filtering, when
// the query carries no fuzzy text, or for the selected row — whose selection band
// already marks it, the same trade the branch picker makes between the highlight
// accent and the selection's own styling.
func (m Model) highlightMatch(
	title, ref string,
	selected bool,
) (string, string) {
	if !m.filter.active || selected {
		return title, ref
	}
	_, text := parseFilterQuery(m.filter.input.Value())
	return m.highlightFuzzy(title, ref, text)
}

// highlightFuzzy accents the characters of a title and ref that text matches as
// a fuzzy subsequence, leaving either untouched when it does not match at all.
// It is the one place the per-workspace filter and the global jump agree on how
// a match is emphasised; an empty query highlights nothing.
func (m Model) highlightFuzzy(title, ref, text string) (string, string) {
	if text == "" {
		return title, ref
	}
	if _, pos, ok := component.FuzzyScore(text, title); ok {
		title = component.Highlight(m.theme, title, pos)
	}
	if _, pos, ok := component.FuzzyScore(text, ref); ok {
		ref = component.Highlight(m.theme, ref, pos)
	}
	return title, ref
}

// tabbedRightPane renders the right pane through the Tabbed component. The root
// computes the per-tab facts from the selected session — whether the previewed
// session is running, and the Diff/Terminal projections — and Tabbed frames the
// tab strip over the active pane's body. contentW and bodyH are the content
// width and the full body height the pane must fill so it lines up with the
// sessions pane.
func (m Model) tabbedRightPane(contentW, bodyH int) string {
	s := m.selectedSession()
	running := s != nil && s.Status == registry.StatusRunning
	return m.tabbed.Body(
		m.theme, contentW, bodyH, running,
		m.overviewSession(s), m.diffSession(s), m.termSession(s),
	)
}

// overviewSession projects the selected session and everything the cockpit has
// derived about it — runtime status, churn, recording, the newest checkpoint,
// path collisions — into the facts the Overview tab draws. It is the one place
// the natively drawn pane is fed, so the pane itself stays free of the
// registry.
func (m Model) overviewSession(s *registry.Session) pane.OverviewSession {
	if s == nil {
		return pane.OverviewSession{}
	}
	title, _ := sessionLabel(s)
	rs := m.runtimeStatus(s)
	ov := pane.OverviewSession{
		Selected:     true,
		Title:        title,
		Agent:        orUnknown(s.Program),
		Profile:      s.ProfileName,
		Status:       rs.Label(),
		StatusStyle:  dotStyle(m.theme, rs),
		StatusIcon:   dotIcon(rs),
		Branch:       s.Branch,
		WorktreePath: s.WorktreePath,
		WorkingDir:   s.WorkingDir,
		BaseCommit:   s.BaseCommit,
		Backend:      s.TmuxName,
		Started:      s.CreatedAt,
		ExitCode:     s.ExitCode,
		Recording:    m.recording[s.WorkspaceID],
		ResumedFrom:  s.ResumedFrom,
	}
	if c, ok := m.churn[s.ID]; ok {
		ov.Added, ov.Removed = c.added, c.removed
		ov.Churned = c.added != 0 || c.removed != 0
	}
	if e, ok := m.recorded[s.ID]; ok {
		ov.Recorded = true
		ov.Commits = len(e.Meta.Commits)
		ov.LastRecord = e.When
	}
	for _, o := range m.collisions[s.ID] {
		name := o.SessionID
		if other, ok := m.reg.Session(o.SessionID); ok {
			name, _ = sessionLabel(other)
		}
		ov.Collisions = append(ov.Collisions, name)
	}
	return ov
}

// orUnknown names a field the registry left empty, so the overview reads as a
// missing fact rather than a blank line.
func orUnknown(s string) string {
	if s == "" {
		return "—"
	}
	return s
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

// termSession projects the Terminal tab's current target into the minimal facts
// its body needs to choose its render state: the selected session's companion,
// or the active workspace's root shell when no session is selected.
func (m Model) termSession(s *registry.Session) pane.TermSession {
	base, _ := m.termTarget(s)
	if base == "" {
		return pane.TermSession{}
	}
	return pane.TermSession{
		CompanionName: companionName(base),
		Root:          s == nil,
	}
}

// menuBar is the footer's key hints: only what applies to the cockpit's
// current state, ending in the pointer at the help overlay. The full keymap
// lives behind that key (see help.go) rather than across the footer, so the
// bar stays short enough to read at a glance instead of becoming a wall of
// glyphs the eye skips.
func (m Model) menuBar() string {
	items := m.menuItems()
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = m.theme.MenuKeyStyle.Render(it[0]) + " " +
			m.theme.MenuDescStyle.Render(it[1])
	}
	return " " + strings.Join(parts, m.theme.MenuSepStyle.Render(menuSep))
}

// menuItems picks the handful of hints worth showing now: what to do with the
// selected session given its status, how to make another, the pane cycle, and
// help. An action that cannot apply — killing an exited session, filtering a
// list of one — is left out rather than shown dead.
func (m Model) menuItems() [][2]string {
	items := make([][2]string, 0, 6)
	switch s := m.selectedSession(); {
	case s != nil:
		items = append(items, m.selectionItems(s)...)
	case m.tabbed.Active() == pane.TabTerminal && m.currentWorkspace() != nil:
		items = append(
			items, [2]string{m.menuKey(config.ActionAttach), "shell"},
		)
	}
	items = append(items, [2]string{m.menuKey(config.ActionNew), "new"})
	if len(m.sessions()) > 1 {
		items = append(
			items, [2]string{m.menuKey(config.ActionFilter), "filter"},
		)
	}
	items = append(
		items, [2]string{m.menuKey(config.ActionPaneTab), "panes"},
	)
	return append(items, m.helpHint())
}

// selectionItems are the hints that depend on the selected session's runtime
// status: a live session offers attach and kill, a paused one resume, an
// exited one delete.
func (m Model) selectionItems(s *registry.Session) [][2]string {
	switch m.runtimeStatus(s) {
	case sessionstatus.Paused:
		return [][2]string{
			{m.menuKey(config.ActionResume), "resume"},
			{m.menuKey(config.ActionDelete), "delete"},
		}
	case sessionstatus.Exited:
		return [][2]string{
			{m.menuKey(config.ActionDelete), "delete"},
		}
	default:
		return [][2]string{
			{m.menuKey(config.ActionAttach), "attach"},
			{m.menuKey(config.ActionKill), "kill"},
		}
	}
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
	if line := m.collisionLine(); line != "" {
		return line
	}
	return ""
}

// collisionLine reports, for the selected session, which live sessions it
// shares changed paths with and which paths — "also edited by <session> —
// foo.go, bar.go" — so the overlap named by the row's badge (see sessionRow)
// is explained without leaving the list. It returns "" when nothing is
// selected or the selected session collides with nothing, and only backs the
// status line when there is no error or status message to take priority over
// it.
func (m Model) collisionLine() string {
	s := m.selectedSession()
	if s == nil {
		return ""
	}
	overlaps := m.collisions[s.ID]
	if len(overlaps) == 0 {
		return ""
	}
	parts := make([]string, 0, len(overlaps))
	for _, ov := range overlaps {
		name := ov.SessionID
		if other, ok := m.reg.Session(ov.SessionID); ok {
			title, _ := sessionLabel(other)
			name = title
		}
		parts = append(parts, fmt.Sprintf(
			"also edited by %s — %s", name, strings.Join(ov.Paths, ", "),
		))
	}
	return m.theme.ErrorStyle.Render(" " + strings.Join(parts, "; "))
}

func (m Model) compactView() string {
	parts := []string{
		m.tabBar(),
		"",
		m.sessionList(
			max(m.width, m.cfg.Layout.CompactWidth),
			max(m.height-layout.ChromeRows, sessionRowLines),
		),
		m.footer(),
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
func confirmBody(theme theme.Theme, prompt string, s *registry.Session) string {
	_, ref := sessionLabel(s)
	return prompt + "\n\n" + theme.DimStyle.Render(
		fmt.Sprintf("%s %s · %s", branchIcon, ref, s.ProfileName),
	)
}

// statusDot renders a session's status glyph in its status colour. It is the
// plain form, with no selection band behind it; a list row uses rowDot, which
// carries the band.
func statusDot(theme theme.Theme, s sessionstatus.Status) string {
	return dotStyle(theme, s).Render(dotIcon(s))
}

// dotStyle is the colour a status is drawn in, and dotIcon its glyph. They are
// split from statusDot so a caller that must compose the dot onto a background
// — a selected row — can restyle it without re-deriving the pairing.
func dotStyle(theme theme.Theme, s sessionstatus.Status) lipgloss.Style {
	switch s {
	case sessionstatus.Waiting:
		return theme.WaitingDotStyle
	case sessionstatus.Idle:
		return theme.IdleDotStyle
	case sessionstatus.Exited, sessionstatus.Paused:
		return theme.ExitedDotStyle
	default:
		return theme.RunningDotStyle
	}
}

func dotIcon(s sessionstatus.Status) string {
	switch s {
	case sessionstatus.Waiting:
		return waitingIcon
	case sessionstatus.Idle:
		return idleIcon
	case sessionstatus.Exited:
		return exitedIcon
	case sessionstatus.Paused:
		return pausedIcon
	default:
		return runningIcon
	}
}

func noWorkspaceBanner(theme theme.Theme, addKey string) string {
	return theme.BannerStyle.Render("No workspaces yet.") + "\n\n" +
		theme.DimStyle.Render(
			"Press n to start a plain session here.\n\n"+
				"Press "+addKey+" to add a git repo as a workspace,\n"+
				"or run wasa inside a git repo.",
		)
}

// orphanEmptyBanner is the empty-state for the permanent "(no workspace)" scratch
// tab: it names the tab's purpose — a plain session in any folder, owned by no
// workspace — so an empty scratch tab reads as a deliberate home rather than a
// stray.
func orphanEmptyBanner(theme theme.Theme) string {
	return theme.BannerStyle.Render("No scratch sessions.") + "\n\n" +
		theme.DimStyle.Render(
			"Press n to start a plain session in any folder —\n"+
				"a scratch session that belongs to no workspace.",
		)
}

func noSessionBanner(theme theme.Theme, name string) string {
	title := "No sessions here."
	if name != "" {
		title = fmt.Sprintf("No sessions in %s.", name)
	}
	return theme.BannerStyle.Render(title) + "\n\n" +
		theme.DimStyle.Render("Press n to create one.")
}

// afterListChange is the command run after a list-mode key that may have moved
// the selection or switched the active pane tab. It re-targets the preview
// stream (tearing it down off the Preview tab) and, on the Terminal tab, kicks
// an immediate companion ensure+capture so switching to it or moving the cursor
// shows the shell without waiting for the next tick.
func (m *Model) afterListChange() tea.Cmd {
	cmd := m.tabbed.Preview.SetTarget(m.previewTarget())
	switch m.tabbed.Active() {
	case pane.TabTerminal:
		cmd = tea.Batch(cmd, m.ensureTermCmd())
	case pane.TabDiff:
		cmd = tea.Batch(cmd, m.ensureDiffCmd())
	}
	return cmd
}

// enterPick opens the directory tree browser over the create form. It roots the
// tree at the parent of whatever the Directory field currently holds — so the
// browser opens among that directory's siblings with the cursor on it — falling
// back to the working directory (see pickerRoot) when the field is empty or names
// no real directory.
func (m Model) enterPick() (tea.Model, tea.Cmd) {
	sel := m.form.Dir()
	rootPath := m.pickerRoot()
	if sel != "" {
		if fi, err := os.Stat(sel); err == nil && fi.IsDir() {
			rootPath = filepath.Dir(sel)
		}
	}
	m.picker = component.NewDirectoryPicker(
		m.theme, rootPath, sel, m.osHome, m.recentDirs(),
		m.pickerWidth(), m.pickerHeight(),
	)
	m.mode = modePick
	return m, textinput.Blink
}

// recentDirs gathers the most-recently-used directories for the picker's recent
// pane: each workspace's repository (by last use) and each session's working
// directory (by creation), merged newest-first, deduplicated and capped.
func (m Model) recentDirs() []component.RecentDir {
	type item struct {
		path string
		at   time.Time
	}
	var items []item
	for _, w := range m.workspaces {
		if w.RepoPath != "" {
			items = append(items, item{w.RepoPath, w.LastUsedAt})
		}
	}
	for _, s := range m.reg.ListSessions() {
		if s.WorkingDir != "" {
			items = append(items, item{s.WorkingDir, s.CreatedAt})
		}
	}
	slices.SortStableFunc(items, func(a, b item) int {
		return b.at.Compare(a.at)
	})

	seen := make(map[string]bool)
	var out []component.RecentDir
	for _, it := range items {
		p := filepath.Clean(it.path)
		if p == "" || p == "." || seen[p] {
			continue
		}
		seen[p] = true
		out = append(
			out,
			component.RecentDir{
				Path:    p,
				Display: component.HomeRel(p, m.osHome),
			},
		)
		if len(out) >= component.MaxRecents {
			break
		}
	}
	return out
}

// updatePick routes input to the open directory browser, forwarding the command
// that carries its decision back to the top-level Update.
func (m Model) updatePick(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.picker, cmd = m.picker.Update(msg)
	return m, cmd
}

// enterBranchPick opens the branch picker over the create form, listing the
// branches of the repository that contains the Directory field's current value
// (or the launch repository when that field is empty). It re-resolves that repo
// on open so the list reflects the directory as currently chosen. When the chosen
// directory is not inside a git repository it is a no-op — the form disables the
// Branch field there, so this should not be reached, but it guards the path
// rather than assuming it.
func (m Model) enterBranchPick() (tea.Model, tea.Cmd) {
	m.form.SyncBranchRepo()
	if !m.form.BranchEnabled() {
		return m, nil
	}
	m.branch = component.NewBranchPicker(
		m.theme, repoBranches(m.form.BranchRepo),
		m.pickerWidth(), m.pickerHeight(),
	)
	m.mode = modePickBranch
	return m, textinput.Blink
}

// updateBranchPick routes input to the open branch picker, forwarding the
// command that carries its decision back to the top-level Update.
func (m Model) updateBranchPick(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.branch, cmd = m.branch.Update(msg)
	return m, cmd
}

// repoBranches lists the local branches of the repository at repoPath, newest
// first, for the branch picker. Errors are swallowed to an empty list: the
// picker still lets a new branch name be typed.
func repoBranches(repoPath string) []string {
	branches, err := worktree.New(repoPath, "", "").Branches()
	if err != nil {
		return nil
	}
	return branches
}

// pickerRoot is the directory the browser opens at when it has no specific seed:
// the current working directory — where wasa was launched, and the user's most
// likely starting point — falling back to the OS home when the cwd is
// unavailable. Rooting at the cwd matters under WSL launched from Windows, where
// the home directory (e.g. /root) sits in a different subtree from the
// /mnt/<drive> paths the user's repositories live under: starting at the cwd
// keeps those repos a few "-" ascents away rather than across the whole tree.
func (m Model) pickerRoot() string {
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		return cwd
	}
	return m.osHome
}

// pickerWidth sizes the browser box to the terminal, clamped to a comfortable
// range so it neither overflows a narrow window nor sprawls on a wide one.
func (m Model) pickerWidth() int {
	return min(max(m.width-8, 48), 96)
}

// pickerHeight is how many tree rows the browser may show, bounded by the
// terminal height and the picker's own row cap.
func (m Model) pickerHeight() int {
	return min(max(m.height-8, 3), component.PickerRows)
}

// configRows is how many rows the settings panel may show; the editor scrolls its
// field list within this height.
func (m Model) configRows() int {
	return max(m.height-8, 5)
}

// sessionDir is the directory a session's companion shell runs in: its worktree
// for a worktree session, or its working directory for a plain one.
func sessionDir(s *registry.Session) string {
	if s.WorktreePath != "" {
		return s.WorktreePath
	}
	return s.WorkingDir
}

// companionName is the deterministic tmux name of a session's companion shell:
// its agent TmuxName with a _term suffix. Deriving it from the stable TmuxName
// keeps it identical across cockpit restarts and distinct from the agent
// session, so the two never collide.
func companionName(sessionTmux string) string {
	return sessionTmux + "_term"
}

// previewTarget is the tmux name the preview should track: the selected
// session's, or "" when nothing running is selected or the Preview tab is not
// the active right-pane tab. Gating on the active tab is what keeps the
// streaming preview's cost off the other tabs — when Diff or Terminal is shown
// the watcher tears down and no per-tick capture runs for the preview.
func (m Model) previewTarget() string {
	if m.tabbed.Active() != pane.TabPreview {
		return ""
	}
	s := m.selectedSession()
	if s == nil || s.Status != registry.StatusRunning {
		return ""
	}
	return s.TmuxName
}

// paneTick is the per-tick work for the active right-pane tab. The Preview and
// Diff tabs poll-or-reconnect the preview stream — a near no-op off the Preview
// tab, since previewTarget is then empty — while the Terminal tab ensures the
// selected session's companion shell exists and re-captures it.
func (m *Model) paneTick() tea.Cmd {
	if m.tabbed.Active() == pane.TabTerminal {
		return m.ensureTermCmd()
	}
	return m.tabbed.Preview.PollOrReconnect(m.previewTarget())
}

// termTarget is the base tmux name and working directory of the shell the
// Terminal tab shows: the selected session's companion in its worktree, or —
// with no session selected — the active workspace's own shell at the repository
// root, so the tab is still a usable prompt for git and friends on an empty or
// unselected list. Both are empty on the orphan tab, which has no repository.
func (m Model) termTarget(s *registry.Session) (string, string) {
	if s != nil {
		return s.TmuxName, sessionDir(s)
	}
	ws := m.currentWorkspace()
	if ws == nil {
		return "", ""
	}
	return registry.WorkspaceTmuxName(ws.ID), ws.RepoPath
}

// ensureTermCmd ensures and captures the Terminal tab's current shell. With no
// target at all it clears the body via an empty base.
func (m *Model) ensureTermCmd() tea.Cmd {
	base, dir := m.termTarget(m.selectedSession())
	return m.tabbed.Terminal.EnsureCmd(base, dir, m.tmux)
}

// applyTerm routes a companion capture to the Terminal pane, passing the
// expected companion of the current target so a stale delivery is dropped,
// and surfaces a spawn or address error on the status line.
func (m *Model) applyTerm(msg pane.TermMsg) tea.Cmd {
	expected := ""
	if base, _ := m.termTarget(m.selectedSession()); base != "" {
		expected = companionName(base)
	}
	cmd, err := m.tabbed.Terminal.Apply(msg, expected)
	if err != nil {
		m.err = err
	}
	return cmd
}

// rightPaneSize is the inner width and height of the right pane's body, below
// the tab strip — the area the Preview, Diff and Terminal tabs render into. It
// mirrors the sizing Tabbed.Body applies to the pane: the tab row consumes two
// rows of the body height above the content window.
func (m Model) rightPaneSize() (w, h int) {
	f := m.frame()
	return f.Right, max(f.Body-layout.PaneTabRows, 1)
}

// sizeDiffViewport sizes the diff viewport to the pane body, so its paging math
// and render match what the Diff body draws. It runs on resize and whenever a
// diff is loaded, never per tick.
func (m *Model) sizeDiffViewport() {
	w, h := m.rightPaneSize()
	m.tabbed.Diff.Size(w, h)
}

// ensureDiffCmd returns a command that computes the selected worktree session's
// diff against its recorded base commit, when that diff is not already loaded.
// It is a no-op (nil) when the diff for the selection is already shown, so it
// fires only on a selection change or a switch to the Diff tab, never per tick.
// The root resolves the session's workspace repository here and passes the
// worktree inputs to the Diff pane, which keeps the already-loaded guard and
// runs the diff. A worktree session whose workspace it cannot resolve surfaces
// that as the diff error rather than running a diff against an empty repo path.
func (m *Model) ensureDiffCmd() tea.Cmd {
	s := m.selectedSession()
	if s == nil {
		return nil
	}
	if s.Status == registry.StatusPaused {
		if m.tabbed.Diff.SID() == s.ID {
			return nil
		}
		sid := s.ID
		return func() tea.Msg {
			return pane.NewDiffErr(
				sid, fmt.Errorf("session is paused; resume it to see its diff"),
			)
		}
	}
	if s.Branch != "" && s.WorktreePath != "" && s.BaseCommit != "" {
		ws, ok := m.reg.Workspace(s.WorkspaceID)
		if !ok {
			if m.tabbed.Diff.SID() == s.ID {
				return nil
			}
			sid := s.ID
			return func() tea.Msg {
				return pane.NewDiffErr(sid, fmt.Errorf("workspace not found"))
			}
		}
		return m.tabbed.Diff.EnsureCmd(
			s.ID, ws.RepoPath, m.home, s.WorkspaceID,
			s.WorktreePath, s.BaseCommit,
		)
	}
	return m.tabbed.Diff.EnsureCmd(
		s.ID, "", m.home, s.WorkspaceID, s.WorktreePath, s.BaseCommit,
	)
}

// refreshDiffCmd recomputes the selected worktree session's diff on the churn
// tick so the pane reflects the agent's ongoing edits in place. It runs only
// while the Diff tab is the active right-pane tab and a worktree session is
// selected — off the Diff tab, on a plain or paused session, or with no
// selection it is a no-op — and routes through the Diff pane's RefreshCmd,
// which bypasses the
// already-loaded guard EnsureCmd keeps for selection changes. A worktree session
// whose workspace cannot be resolved is left showing its last diff rather than
// recomputed against an empty repo path.
func (m *Model) refreshDiffCmd() tea.Cmd {
	if m.tabbed.Active() != pane.TabDiff {
		return nil
	}
	s := m.selectedSession()
	if s == nil || s.Branch == "" || s.WorktreePath == "" ||
		s.BaseCommit == "" || s.Status == registry.StatusPaused {
		return nil
	}
	ws, ok := m.reg.Workspace(s.WorkspaceID)
	if !ok {
		return nil
	}
	return m.tabbed.Diff.RefreshCmd(
		s.ID, ws.RepoPath, m.home, s.WorkspaceID, s.WorktreePath, s.BaseCommit,
	)
}

// applyDiff routes a computed diff to the Diff pane, dropping a delivery whose
// session is no longer selected so a slow diff cannot overwrite the body after
// the cursor moved. It sizes the viewport before applying so the paging math
// matches the current pane.
func (m *Model) applyDiff(msg pane.DiffMsg) tea.Cmd {
	if s := m.selectedSession(); s == nil || s.ID != msg.SessionID() {
		return nil
	}
	m.sizeDiffViewport()
	return m.tabbed.Diff.Apply(msg)
}

// orphanTabName labels the synthetic tab that collects sessions belonging to no
// workspace — plain sessions launched outside any registered repository. Its id
// is the empty string, the WorkspaceID those sessions carry, so selecting it
// lists exactly them through the same workspaceSessions filter.
const orphanTabName = "(no workspace)"

// tabInfo is one cockpit tab: a workspace, or the synthetic orphan tab. id is the
// workspace id ("" for the orphan tab) that workspaceSessions filters sessions by.
type tabInfo struct {
	id   string
	name string
}

// tabList is the ordered set of cockpit tabs: one per workspace (most-recently-
// used first), followed by the synthetic "(no workspace)" scratch tab. That tab
// is a permanent home for plain sessions that belong to no workspace, so it is
// always present once there is anything to anchor against — any workspace or any
// orphan session — giving scratch-session creation a reachable front door even
// before the first orphan session exists. It is omitted only at the true cold
// start (no workspaces and no sessions), where the empty-state banner onboards
// instead, so tabList is empty in exactly that case.
func (m Model) tabList() []tabInfo {
	tabs := make([]tabInfo, 0, len(m.workspaces)+1)
	for _, w := range m.workspaces {
		tabs = append(tabs, tabInfo{id: w.ID, name: w.Name})
	}
	if len(m.workspaces) > 0 || m.hasOrphanSessions() {
		tabs = append(tabs, tabInfo{id: "", name: orphanTabName})
	}
	return tabs
}

// hasOrphanSessions reports whether any session belongs to no workspace, which is
// what makes the synthetic orphan tab appear.
func (m Model) hasOrphanSessions() bool {
	for _, s := range m.reg.ListSessions() {
		if s.WorkspaceID == "" {
			return true
		}
	}
	return false
}

func (m *Model) cycleTab(delta int) {
	tabs := m.tabList()
	n := len(tabs)
	if n == 0 {
		return
	}
	i := max(m.tabIndex(), 0)
	i = (i + delta%n + n) % n
	m.activeID = tabs[i].id
	m.cursor = 0
}

func (m Model) tabIndex() int {
	for i, t := range m.tabList() {
		if t.id == m.activeID {
			return i
		}
	}
	return -1
}

func (m Model) currentWorkspace() *registry.Workspace {
	for _, w := range m.workspaces {
		if w.ID == m.activeID {
			return w
		}
	}
	return nil
}

func (m Model) hasWorkspace(id string) bool {
	for _, w := range m.workspaces {
		if w.ID == id {
			return true
		}
	}
	return false
}

// hasTab reports whether id names a current tab — a workspace or, for "", the
// orphan tab when it exists. refresh uses it to decide whether the active tab
// survived a registry change before falling back to the first tab.
func (m Model) hasTab(id string) bool {
	for _, t := range m.tabList() {
		if t.id == id {
			return true
		}
	}
	return false
}

// workspaceSessions returns the active workspace's sessions in storage order,
// before any filter is applied.
func (m Model) workspaceSessions() []*registry.Session {
	var out []*registry.Session
	for _, s := range m.reg.ListSessions() {
		if s.WorkspaceID == m.activeID {
			out = append(out, s)
		}
	}
	return out
}

// sessions returns the sessions the cockpit list currently shows: the active
// workspace's sessions, narrowed by the active filter query when filtering. It is
// the one view every list operation reads through — cursor bounds, selection,
// preview targeting — so a filter narrows them all at once while leaving the
// registry untouched.
func (m Model) sessions() []*registry.Session {
	ss := m.workspaceSessions()
	if !m.filter.active {
		return ss
	}
	status, text := parseFilterQuery(m.filter.input.Value())
	out := make([]*registry.Session, 0, len(ss))
	for _, s := range ss {
		if matchesFilter(s, status, text) {
			out = append(out, s)
		}
	}
	return out
}

func (m Model) selectedSession() *registry.Session {
	ss := m.sessions()
	if m.cursor < 0 || m.cursor >= len(ss) {
		return nil
	}
	return ss[m.cursor]
}
