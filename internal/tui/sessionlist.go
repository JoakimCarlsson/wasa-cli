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

	"github.com/joakimcarlsson/wasa/internal/config"
	"github.com/joakimcarlsson/wasa/internal/registry"
	"github.com/joakimcarlsson/wasa/internal/sessionstatus"
	"github.com/joakimcarlsson/wasa/internal/tui/component"
	"github.com/joakimcarlsson/wasa/internal/tui/pane"
	"github.com/joakimcarlsson/wasa/internal/worktree"
)

// paneTab selects which view the right pane shows: the live preview (today's
// default), a git diff of the session's work, or a companion shell. Only the
// active tab does per-tick work; the others are idle, so cycling away from
// Preview tears its stream down and cycling back resumes it.
type paneTab int

const (
	panePreview paneTab = iota
	paneDiff
	paneTerminal
)

// paneTabNames is the tab strip's labels in paneTab order.
var paneTabNames = [...]string{"Preview", "Diff", "Terminal"}

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

// afterListChange is the command run after a list-mode key that may have moved
// the selection or switched the active pane tab. It re-targets the preview
// stream (tearing it down off the Preview tab) and, on the Terminal tab, kicks
// an immediate companion ensure+capture so switching to it or moving the cursor
// shows the shell without waiting for the next tick.
func (m *Model) afterListChange() tea.Cmd {
	cmd := m.preview.SetTarget(m.previewTarget())
	switch m.pane {
	case paneTerminal:
		cmd = tea.Batch(cmd, m.ensureTermCmd())
	case paneDiff:
		cmd = tea.Batch(cmd, m.ensureDiffCmd())
	}
	return cmd
}

// enterPick opens the directory tree browser over the create form. It roots the
// tree at the parent of whatever the Directory field currently holds — so the
// browser opens among that directory's siblings with the cursor on it — falling
// back to $HOME, then the working directory, when the field is empty or names no
// real directory.
func (m Model) enterPick() (tea.Model, tea.Cmd) {
	sel := m.form.Dir()
	rootPath := m.osHome
	if sel != "" {
		if fi, err := os.Stat(sel); err == nil && fi.IsDir() {
			rootPath = filepath.Dir(sel)
		}
	}
	if rootPath == "" {
		if cwd, err := os.Getwd(); err == nil {
			rootPath = cwd
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
			component.RecentDir{Path: p, Display: component.HomeRel(p, m.osHome)},
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
	if m.pane != panePreview {
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
	if m.pane == paneTerminal {
		return m.ensureTermCmd()
	}
	return m.preview.PollOrReconnect(m.previewTarget())
}

// ensureTermCmd ensures and captures the selected session's companion shell for
// the Terminal tab. With no session selected it clears the body via an empty
// target.
func (m *Model) ensureTermCmd() tea.Cmd {
	s := m.selectedSession()
	if s == nil {
		return m.term.EnsureCmd("", "", m.tmux)
	}
	return m.term.EnsureCmd(s.TmuxName, sessionDir(s), m.tmux)
}

// applyTerm routes a companion capture to the Terminal pane, passing the
// expected companion of the current selection so a stale delivery is dropped,
// and surfaces a spawn or address error on the status line.
func (m *Model) applyTerm(msg pane.TermMsg) tea.Cmd {
	expected := ""
	if s := m.selectedSession(); s != nil {
		expected = companionName(s.TmuxName)
	}
	cmd, err := m.term.Apply(msg, expected)
	if err != nil {
		m.err = err
	}
	return cmd
}

// rightPaneSize is the inner width and height of the right pane's body, below
// the tab strip — the area the Preview, Diff and Terminal tabs render into. It
// mirrors the sizing listView applies to the pane.
func (m Model) rightPaneSize() (w, h int) {
	bodyH := max(m.height-chromeRows, 3)
	return m.width - m.listColWidth() - 4, max(bodyH-(tabRowRows-1), 1)
}

// sizeDiffViewport sizes the diff viewport to the pane body, so its paging math
// and render match what the Diff body draws. It runs on resize and whenever a
// diff is loaded, never per tick.
func (m *Model) sizeDiffViewport() {
	w, h := m.rightPaneSize()
	m.diff.Size(w, h)
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
			if m.diff.SID() == s.ID {
				return nil
			}
			sid := s.ID
			return func() tea.Msg {
				return pane.NewDiffErr(sid, fmt.Errorf("workspace not found"))
			}
		}
		return m.diff.EnsureCmd(
			s.ID, ws.RepoPath, m.home, s.WorkspaceID,
			s.WorktreePath, s.BaseCommit,
		)
	}
	return m.diff.EnsureCmd(
		s.ID, "", m.home, s.WorkspaceID, s.WorktreePath, s.BaseCommit,
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
	return m.diff.Apply(msg)
}

func (m *Model) cycleTab(delta int) {
	n := len(m.workspaces)
	if n == 0 {
		return
	}
	i := max(m.tabIndex(), 0)
	i = (i + delta%n + n) % n
	m.activeID = m.workspaces[i].ID
	m.cursor = 0
}

// cyclePaneTab advances the active right-pane tab by delta, wrapping. The list
// update that calls it then re-runs ensureWatcher, which tears the preview
// stream down when the new tab is not Preview and re-establishes it on return.
func (m *Model) cyclePaneTab(delta int) {
	n := len(paneTabNames)
	m.pane = paneTab(((int(m.pane)+delta)%n + n) % n)
}

func (m Model) tabIndex() int {
	for i, w := range m.workspaces {
		if w.ID == m.activeID {
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

// sessions returns the active workspace's sessions in storage order.
func (m Model) sessions() []*registry.Session {
	var out []*registry.Session
	for _, s := range m.reg.ListSessions() {
		if s.WorkspaceID == m.activeID {
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
