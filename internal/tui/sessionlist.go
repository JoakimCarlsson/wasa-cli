package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/joakimcarlsson/wasa/internal/config"
	"github.com/joakimcarlsson/wasa/internal/registry"
	"github.com/joakimcarlsson/wasa/internal/sessionstatus"
	"github.com/joakimcarlsson/wasa/internal/tui/component"
	"github.com/joakimcarlsson/wasa/internal/tui/pane"
	"github.com/joakimcarlsson/wasa/internal/tui/theme"
	"github.com/joakimcarlsson/wasa/internal/worktree"
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
	if m.mode == modePickWorkspace {
		return component.Modal(m.picker.View(), base)
	}
	if m.mode == modeConfirm {
		return component.Modal(m.confirm.View(), base)
	}
	if m.mode == modeConfig {
		return component.Modal(m.editor.View(), base)
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
	tabs := m.tabList()
	if len(tabs) == 0 {
		return m.theme.InactiveTabStyle.Render("no workspaces")
	}
	active := m.tabIndex()
	parts := make([]string, len(tabs))
	for i, t := range tabs {
		if i == active {
			parts[i] = m.theme.ActiveTabStyle.Render(t.name)
		} else {
			parts[i] = m.theme.InactiveTabStyle.Render(t.name)
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Bottom, parts...)
}

func (m Model) sessionList(paneW int) string {
	if len(m.tabList()) == 0 {
		return noWorkspaceBanner(m.theme, m.menuKey(config.ActionWorkspaceAdd))
	}

	ss := m.sessions()
	if m.filter.active {
		return m.filter.input.View() + "\n\n" + m.filterBody(ss, paneW)
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
	return m.sessionRows(ss, paneW)
}

// filterBody is the list body while filtering: the matched rows, or a clear
// "no matches" line when the query narrows the list to nothing — so the pane
// reads as deliberately empty rather than blank.
func (m Model) filterBody(ss []*registry.Session, paneW int) string {
	if len(ss) == 0 {
		return m.theme.DimStyle.Render("  no matches")
	}
	return m.sessionRows(ss, paneW)
}

// sessionRows renders the session list body: each session as a two-line row,
// numbered from one in the order shown.
func (m Model) sessionRows(ss []*registry.Session, paneW int) string {
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
	title, ref = m.highlightMatch(title, ref, selected)
	rs := m.runtimeStatus(s)
	prefix := fmt.Sprintf(" %d ", i+1)
	head := fmt.Sprintf("%s%s %s", prefix, statusDot(m.theme, rs), title)

	return lipgloss.JoinVertical(
		lipgloss.Left,
		titleS.Render(component.PadAnsi(head, w)),
		m.subLine(s, ref, rs, descS, selected, w),
	)
}

// subLine renders a row's detail line — the branch ref, an optional coloured
// +N/−M churn token, then the profile and status — padded to w. The churn token
// carries its own add/remove colours, so the line is composed from segments each
// rendered with descS rather than one descS.Render over the whole string:
// embedding the token's colour reset inside a single Render would cut the
// selection band short for everything after it on the selected row. When the
// churn token would not fit, the plain line is rendered instead so a row never
// overflows its column.
func (m Model) subLine(
	s *registry.Session,
	ref string,
	rs sessionstatus.Status,
	descS lipgloss.Style,
	selected bool,
	w int,
) string {
	plain := fmt.Sprintf(
		"   %s %s · %s · %s", branchIcon, ref, s.ProfileName, rs.Label(),
	)
	churn := m.churnToken(s, selected)
	if churn == "" {
		return descS.Render(component.PadAnsi(plain, w))
	}

	pre := fmt.Sprintf("   %s %s ", branchIcon, ref)
	post := fmt.Sprintf(" · %s · %s", s.ProfileName, rs.Label())
	used := ansi.StringWidth(pre) + ansi.StringWidth(churn) +
		ansi.StringWidth(post)
	if used > w {
		return descS.Render(component.PadAnsi(plain, w))
	}
	if tail := w - used; tail > 0 {
		post += strings.Repeat(" ", tail)
	}
	return descS.Render(pre) + churn + descS.Render(post)
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
		m.theme, contentW, bodyH, running, m.diffSession(s), m.termSession(s),
	)
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
		{m.menuKey(config.ActionFilter), "filter"},
		{m.menuKey(config.ActionWorkspaceAdd), "+ws"},
		{m.menuKey(config.ActionWorkspaceDelete), "-ws"},
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
func confirmBody(theme theme.Theme, prompt string, s *registry.Session) string {
	_, ref := sessionLabel(s)
	return prompt + "\n\n" + theme.DimStyle.Render(
		fmt.Sprintf("%s %s · %s", branchIcon, ref, s.ProfileName),
	)
}

func statusDot(theme theme.Theme, s sessionstatus.Status) string {
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

// ensureTermCmd ensures and captures the selected session's companion shell for
// the Terminal tab. With no session selected it clears the body via an empty
// target.
func (m *Model) ensureTermCmd() tea.Cmd {
	s := m.selectedSession()
	if s == nil {
		return m.tabbed.Terminal.EnsureCmd("", "", m.tmux)
	}
	return m.tabbed.Terminal.EnsureCmd(s.TmuxName, sessionDir(s), m.tmux)
}

// applyTerm routes a companion capture to the Terminal pane, passing the
// expected companion of the current selection so a stale delivery is dropped,
// and surfaces a spawn or address error on the status line.
func (m *Model) applyTerm(msg pane.TermMsg) tea.Cmd {
	expected := ""
	if s := m.selectedSession(); s != nil {
		expected = companionName(s.TmuxName)
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
	bodyH := max(m.height-chromeRows, 3)
	return m.width - m.listColWidth() - 4, max(bodyH-2, 1)
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
// selected — off the Diff tab, on a plain session, or with no selection it is a
// no-op — and routes through the Diff pane's RefreshCmd, which bypasses the
// already-loaded guard EnsureCmd keeps for selection changes. A worktree session
// whose workspace cannot be resolved is left showing its last diff rather than
// recomputed against an empty repo path.
func (m *Model) refreshDiffCmd() tea.Cmd {
	if m.tabbed.Active() != pane.TabDiff {
		return nil
	}
	s := m.selectedSession()
	if s == nil || s.Branch == "" || s.WorktreePath == "" ||
		s.BaseCommit == "" {
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
