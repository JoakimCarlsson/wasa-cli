// Package tui is the wasa cockpit: a Bubble Tea terminal UI that shows one tab
// per workspace (ordered most-recently-used), the sessions of the active
// workspace each with a running/exited status dot, and the create, attach and
// kill actions over them. It does not reimplement orchestration; it drives the
// registry (#20), profiles (#21) and the launch seam (worktree → hook → tmux)
// and reads session status from the reconciled registry rather than polling
// agent output.
//
// Attach is the one sharp edge: it hands the terminal to tmux through
// tea.ExecProcess, never a hand-wired exec.Command, so Bubble Tea suspends its
// renderer for the attach and resumes cleanly on detach (C-b d). See attach.
package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/joakimcarlsson/wasa/internal/backend"
	"github.com/joakimcarlsson/wasa/internal/config"
	"github.com/joakimcarlsson/wasa/internal/launch"
	"github.com/joakimcarlsson/wasa/internal/registry"
	"github.com/joakimcarlsson/wasa/internal/repo"
	"github.com/joakimcarlsson/wasa/internal/sessionstatus"
	"github.com/joakimcarlsson/wasa/internal/tui/component"
	"github.com/joakimcarlsson/wasa/internal/worktree"
)

// mode is the model's interaction mode: browsing the session list or filling in
// the create form.
type mode int

const (
	modeList mode = iota
	modeCreate
	modeConfirm
	modePick
	modePickBranch
	modeConfig
)

// Model is the cockpit's Bubble Tea model. It holds the registry it drives, the
// most-recently-used workspaces snapshot, the active workspace (tracked by id so
// it follows the workspace when an attach or create reorders the tabs), and the
// list cursor. home is $WASA_HOME, the data directory sessions are launched
// against; osHome is the user's home directory, used only to root and abbreviate
// the directory browser — the two are distinct and must not be conflated.
//
// The right pane's preview/diff/terminal state lives in tabs, a tabbedPane that
// owns the three feature machines; the model delegates to it rather than holding
// their state directly.
type Model struct {
	home   string
	osHome string
	reg    *registry.Registry
	tmux   backend.SessionBackend
	cfg    config.Config
	theme  Theme
	help   component.Help
	keys   keymap

	workspaces []*registry.Workspace
	activeID   string
	cursor     int

	mode    mode
	tabs    tabbedPane
	form    createForm
	confirm confirmDialog
	picker  dirPicker
	branch  branchPicker
	editor  configEditor

	confirmCmd tea.Cmd

	width  int
	height int

	now          func() time.Time
	statuses     *sessionstatus.Tracker
	notify       func(title, body string)
	lastNotifyAt map[string]time.Time
	lastStatus   map[string]sessionstatus.Status

	status string
	err    error
}

// New builds a cockpit model over reg. currentID is the workspace for the
// repository wasa was launched in; it becomes the initially active tab when
// present, otherwise the most-recently-used workspace is. cfg is the resolved
// cockpit configuration (theme, keys, layout); pass config.Default for the
// built-in behaviour.
func New(
	home string,
	reg *registry.Registry,
	currentID string,
	cfg config.Config,
) Model {
	be := backend.Default()
	var stream backend.StreamingBackend
	if s, ok := be.(backend.StreamingBackend); ok {
		stream = s
	}
	th := newTheme(cfg.Theme)
	m := Model{
		home:         home,
		reg:          reg,
		tmux:         be,
		cfg:          cfg,
		theme:        th,
		help:         newMenuHelp(th),
		keys:         newKeymap(cfg.Keys),
		tabs:         newTabbedPane(be, stream),
		now:          time.Now,
		statuses:     sessionstatus.NewTracker(time.Now),
		notify:       makeNotifier(cfg.Notify),
		lastNotifyAt: make(map[string]time.Time),
		lastStatus:   make(map[string]sessionstatus.Status),
	}
	m.osHome, _ = os.UserHomeDir()
	m.workspaces = reg.ListWorkspaces()
	switch {
	case currentID != "" && m.hasWorkspace(currentID):
		m.activeID = currentID
	case len(m.workspaces) > 0:
		m.activeID = m.workspaces[0].ID
	}
	return m
}

// Run launches the cockpit over reg and blocks until the user quits. It uses the
// alternate screen so the terminal is restored on exit and around every attach.
// cfg is the resolved cockpit configuration.
func Run(
	home string,
	reg *registry.Registry,
	currentID string,
	cfg config.Config,
) error {
	_, err := tea.NewProgram(
		New(home, reg, currentID, cfg), tea.WithAltScreen(),
	).Run()
	return err
}

// previewInterval is the cadence of the preview fallback tick. On the streaming
// (unix/tmux) path the live control-mode connection drives updates and this tick
// is a near no-op that only reconnects a dropped stream; the old per-tick
// capture-pane subprocess is gone. On a non-streaming backend (Windows) the tick
// is still the poll that re-captures the selected session, preserving the prior
// behaviour. The list's running/exited status always comes from the registry,
// never from this capture.
const previewInterval = 750 * time.Millisecond

// Init implements tea.Model. It fires one immediate tick so the preview opens
// its stream (or polls) right away rather than after the first interval.
func (m Model) Init() tea.Cmd {
	return func() tea.Msg { return tickMsg{} }
}

// emit wraps a ready message value in a command, the idiom for a child
// reporting a result up to its parent through the normal message path.
func emit(msg tea.Msg) tea.Cmd {
	return func() tea.Msg { return msg }
}

type tickMsg struct{}

func tick() tea.Cmd {
	return tea.Tick(
		previewInterval,
		func(time.Time) tea.Msg { return tickMsg{} },
	)
}

type createdMsg struct {
	session *registry.Session
	err     error
}

type killedMsg struct{ err error }

type deletedMsg struct{ err error }

type attachedMsg struct {
	sessionID string
	err       error
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.tabs.setSize(m.rightPaneSize())
		return m, nil

	case tickMsg:
		m.sweepStatuses()
		return m, tea.Batch(tick(), m.tabs.tick(m.selectedSession()))

	case previewMsg, termMsg, diffMsg:
		cmd, err := m.tabs.apply(m.theme, msg, m.selectedSession())
		if err != nil {
			m.err = err
		}
		return m, cmd

	case confirmDecisionMsg:
		return m.applyConfirmDecision(msg)

	case formSubmitMsg, formCancelMsg, formPickDirMsg, formPickBranchMsg:
		return m.applyFormEvent(msg)

	case cfgAppliedMsg, cfgClosedMsg:
		return m.applyConfigEvent(msg)

	case dirPickedMsg, dirCancelledMsg:
		return m.applyDirPick(msg)

	case branchPickedMsg, branchCancelledMsg:
		return m.applyBranchPick(msg)

	case createdMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		if msg.session.Branch != "" {
			m.status = "created session on " + msg.session.Branch
		} else {
			m.status = "created session in " + msg.session.WorkingDir
		}
		return m, m.refresh()

	case killedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.status = "killed session"
		return m, m.refresh()

	case deletedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.status = "deleted session"
		return m, m.refresh()

	case attachedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.reg.MarkAttached(msg.sessionID)
		if err := m.reg.Save(); err != nil {
			m.err = err
			return m, nil
		}
		return m, m.refresh()

	case filterTickMsg:
		if m.mode == modePick {
			return m, m.picker.tickFilter(msg.gen)
		}
		return m, nil

	case filterResultMsg:
		if m.mode == modePick {
			m.picker.applyFilterResult(msg)
		}
		return m, nil
	}

	switch m.mode {
	case modeCreate:
		return m.updateCreate(msg)
	case modeConfirm:
		return m.updateConfirm(msg)
	case modePick:
		return m.updatePick(msg)
	case modePickBranch:
		return m.updateBranchPick(msg)
	case modeConfig:
		return m.updateConfig(msg)
	}
	return m.updateList(msg)
}

func (m Model) updateList(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	m.tabs.setSize(m.rightPaneSize())
	m.tabs.handleKey(msg)

	switch m.keys.action(key.String()) {
	case config.ActionQuit:
		m.tabs.close()
		return m, tea.Quit
	case config.ActionTabNext:
		m.cycleTab(1)
	case config.ActionTabPrev:
		m.cycleTab(-1)
	case config.ActionPaneTab:
		m.tabs.cycle(1)
	case config.ActionCursorUp:
		if m.cursor > 0 {
			m.cursor--
		}
	case config.ActionCursorDown:
		if m.cursor < len(m.sessions())-1 {
			m.cursor++
		}
	case config.ActionNew:
		return m.enterCreate()
	case config.ActionAttach:
		return m.attach()
	case config.ActionKill:
		return m.enterConfirmKill()
	case config.ActionDelete:
		return m.enterConfirmDelete()
	case config.ActionConfig:
		return m.enterConfig()
	}
	return m, m.afterListChange()
}

// afterListChange is the command run after a list-mode key that may have moved
// the selection or switched the active pane tab: it asks the tabbed pane to
// re-target the active feature machine (tearing the preview stream down off the
// Preview tab) so the body reflects the new selection without waiting for the
// next tick.
func (m *Model) afterListChange() tea.Cmd {
	return m.tabs.retarget(m.selectedSession(), m.reg, m.home)
}

// updateCreate routes key input to the create form. The form reports its
// outcomes (submit, cancel, open a picker) up as typed messages handled by the
// top-level Update; here we only keep the profile menu in sync with the
// directory the form currently resolves to.
func (m Model) updateCreate(msg tea.Msg) (tea.Model, tea.Cmd) {
	prevBranchRepo := m.form.branchRepo
	form, cmd := m.form.update(msg)
	m.form = form
	if m.form.branchRepo != prevBranchRepo {
		m.form.setProfiles(m.profilesFor(m.form.branchRepo))
	}
	return m, cmd
}

// applyFormEvent handles an outcome the create form reported. It is a no-op
// outside create mode so a late delivery cannot act on a closed form. Cancel
// returns to the list, the pick events open the directory or branch browser, and
// submit turns the form into a session.
func (m Model) applyFormEvent(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.mode != modeCreate {
		return m, nil
	}
	switch msg.(type) {
	case formCancelMsg:
		m.mode = modeList
		return m, nil
	case formPickDirMsg:
		return m.enterPick()
	case formPickBranchMsg:
		return m.enterBranchPick()
	case formSubmitMsg:
		return m.submitCreate()
	}
	return m, nil
}

// submitCreate turns the create form into a session. A worktree session is
// created against the repository of the chosen Directory — not the active tab —
// so the branch listed in the form and the worktree it creates belong to the
// same repository; its workspace is registered (and reg persisted) when not yet
// known, and the profile is constrained to that workspace's profiles. A plain
// session keeps running in the chosen directory under the active workspace,
// defaulting to the current working directory when no directory was given.
func (m Model) submitCreate() (tea.Model, tea.Cmd) {
	ws := m.currentWorkspace()
	params := m.form.params()
	if params.Branch != "" {
		target, err := m.worktreeWorkspace()
		if err != nil {
			m.err = err
			m.mode = modeList
			return m, nil
		}
		ws = target
		params.Profile = validProfile(ws, params.Profile)
	} else if params.WorkingDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			params.WorkingDir = cwd
		}
	}
	m.mode = modeList
	m.status = "creating session…"
	return m, m.createCmd(ws, params)
}

// worktreeWorkspace resolves the workspace a worktree session must be created
// against: the repository of the chosen Directory, derived from the same
// branchRepo the Branch field already resolved. It registers that repository's
// workspace when it is not yet known so the session lands in the picked repo
// even when wasa was launched elsewhere; createCmd persists reg afterwards.
func (m Model) worktreeWorkspace() (*registry.Workspace, error) {
	repoPath, remoteURL, err := repo.Resolve(m.form.branchRepo)
	if err != nil {
		return nil, err
	}
	ws, _ := repo.Register(m.reg, repoPath, remoteURL)
	return ws, nil
}

// profilesFor returns the profile names the create form should offer for a
// directory whose repository toplevel is branchRepo. An empty branchRepo (plain
// session, or a non-repo directory) keeps the active workspace's profiles. A
// directory inside an already-registered repository offers that workspace's
// profiles; one inside an unregistered repository offers the single default
// profile a fresh workspace would be created with, so the selection stays valid
// for the workspace the worktree lands in.
func (m Model) profilesFor(branchRepo string) []string {
	if branchRepo == "" {
		return profileNames(m.currentWorkspace())
	}
	repoPath, remoteURL, err := repo.Resolve(branchRepo)
	if err != nil {
		return profileNames(m.currentWorkspace())
	}
	if ws, ok := m.reg.Workspace(
		registry.WorkspaceID(repoPath, remoteURL),
	); ok {
		return profileNames(ws)
	}
	return []string{registry.DefaultProfileName}
}

// profileNames lists ws's profile names, or nil when ws is nil (wasa launched
// outside any repository).
func profileNames(ws *registry.Workspace) []string {
	if ws == nil {
		return nil
	}
	names := make([]string, len(ws.Profiles))
	for i, p := range ws.Profiles {
		names[i] = p.Name
	}
	return names
}

// validProfile constrains a chosen profile name to ws: it returns name when ws
// has a profile by that name, and otherwise "" so the workspace default is used
// rather than sending an unknown name that SelectProfile would reject.
func validProfile(ws *registry.Workspace, name string) string {
	if name == "" || ws == nil {
		return ""
	}
	for _, p := range ws.Profiles {
		if p.Name == name {
			return name
		}
	}
	return ""
}

// enterPick opens the directory tree browser over the create form. It roots the
// tree at the parent of whatever the Directory field currently holds — so the
// browser opens among that directory's siblings with the cursor on it — falling
// back to $HOME, then the working directory, when the field is empty or names no
// real directory.
func (m Model) enterPick() (tea.Model, tea.Cmd) {
	sel := m.form.dir()
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
	m.picker = newDirPicker(
		m.theme, rootPath, sel, m.osHome, m.recentDirs(),
		m.pickerWidth(), m.pickerHeight(),
	)
	m.mode = modePick
	return m, textinput.Blink
}

// recentDirs gathers the most-recently-used directories for the picker's recent
// pane: each workspace's repository (by last use) and each session's working
// directory (by creation), merged newest-first, deduplicated and capped.
func (m Model) recentDirs() []recentDir {
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
	var out []recentDir
	for _, it := range items {
		p := filepath.Clean(it.path)
		if p == "" || p == "." || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, recentDir{path: p, display: homeRel(p, m.osHome)})
		if len(out) >= maxRecents {
			break
		}
	}
	return out
}

// updatePick routes input to the open directory browser. The browser reports a
// chosen directory as a dirPickedMsg and a dismissal as a dirCancelledMsg, both
// handled by the top-level Update.
func (m Model) updatePick(msg tea.Msg) (tea.Model, tea.Cmd) {
	picker, cmd := m.picker.update(msg)
	m.picker = picker
	return m, cmd
}

// applyDirPick handles the directory browser's reported outcome: a chosen
// directory writes into the form's Directory field and returns to the form;
// dismissal returns unchanged. It is a no-op outside directory-pick mode.
func (m Model) applyDirPick(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.mode != modePick {
		return m, nil
	}
	switch msg := msg.(type) {
	case dirCancelledMsg:
		m.mode = modeCreate
		return m, textinput.Blink
	case dirPickedMsg:
		m.form.setDir(msg.path)
		m.form.setProfiles(m.profilesFor(m.form.branchRepo))
		m.mode = modeCreate
		return m, textinput.Blink
	}
	return m, nil
}

// enterBranchPick opens the branch picker over the create form, listing the
// branches of the repository that contains the Directory field's current value
// (or the launch repository when that field is empty). It re-resolves that repo
// on open so the list reflects the directory as currently chosen. When the chosen
// directory is not inside a git repository it is a no-op — the form disables the
// Branch field there, so this should not be reached, but it guards the path
// rather than assuming it.
func (m Model) enterBranchPick() (tea.Model, tea.Cmd) {
	m.form.syncBranchRepo()
	if !m.form.branchEnabled() {
		return m, nil
	}
	m.branch = newBranchPicker(
		m.theme, repoBranches(m.form.branchRepo),
		m.pickerWidth(), m.pickerHeight(),
	)
	m.mode = modePickBranch
	return m, textinput.Blink
}

// updateBranchPick routes input to the open branch picker. The picker reports a
// chosen branch as a branchPickedMsg and a dismissal as a branchCancelledMsg,
// both handled by the top-level Update.
func (m Model) updateBranchPick(msg tea.Msg) (tea.Model, tea.Cmd) {
	picker, cmd := m.branch.update(msg)
	m.branch = picker
	return m, cmd
}

// applyBranchPick handles the branch picker's reported outcome: a chosen or
// typed branch writes into the form's Branch field and returns to the form;
// dismissal returns unchanged. It is a no-op outside branch-pick mode.
func (m Model) applyBranchPick(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.mode != modePickBranch {
		return m, nil
	}
	switch msg := msg.(type) {
	case branchCancelledMsg:
		m.mode = modeCreate
		return m, textinput.Blink
	case branchPickedMsg:
		m.form.setBranch(msg.branch)
		m.mode = modeCreate
		return m, textinput.Blink
	}
	return m, nil
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
	return min(max(m.height-8, 3), pickerRows)
}

// enterConfirmDelete opens the delete-confirmation modal for the selected
// session. The delete command captures that session, so a later list change
// cannot retarget it. With no session selected it is a no-op.
func (m Model) enterConfirmDelete() (tea.Model, tea.Cmd) {
	s := m.selectedSession()
	if s == nil {
		return m, nil
	}
	title, _ := sessionLabel(s)
	body := confirmBody(
		m.theme, fmt.Sprintf("Delete %q?\nThis cannot be undone.", title), s,
	)
	return m.enterConfirm(
		newConfirmDialog(m.theme, "Delete session", body, "Delete", "Cancel", true),
		m.deleteCmd(s),
	)
}

// enterConfirmKill opens the kill-confirmation modal for the selected session.
// Like the existing instant kill it applies only to a running session; with no
// session selected or an already-exited one it is a no-op.
func (m Model) enterConfirmKill() (tea.Model, tea.Cmd) {
	s := m.selectedSession()
	if s == nil || s.Status != registry.StatusRunning {
		return m, nil
	}
	title, _ := sessionLabel(s)
	body := confirmBody(
		m.theme, fmt.Sprintf(
			"Kill %q?\nIt stops but stays in the list as exited.", title,
		), s,
	)
	return m.enterConfirm(
		newConfirmDialog(m.theme, "Kill session", body, "Kill", "Cancel", true),
		m.killCmd(s),
	)
}

// enterConfirm opens dialog as a modal and stores onConfirm as the command to
// run if it is accepted.
func (m Model) enterConfirm(
	dialog confirmDialog,
	onConfirm tea.Cmd,
) (tea.Model, tea.Cmd) {
	m.confirm = dialog
	m.confirmCmd = onConfirm
	m.mode = modeConfirm
	m.err = nil
	m.status = ""
	return m, nil
}

// updateConfirm routes key input to the active confirm modal. The dialog
// reports the user's choice up as a confirmDecisionMsg command, handled by the
// top-level Update.
func (m Model) updateConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	dialog, cmd := m.confirm.update(msg)
	m.confirm = dialog
	return m, cmd
}

// applyConfirmDecision handles the confirm modal's reported choice: accepting it
// runs the stored command; cancelling returns to the list with no change. It is
// a no-op outside confirm mode so a late delivery cannot fire the action twice.
func (m Model) applyConfirmDecision(msg confirmDecisionMsg) (tea.Model, tea.Cmd) {
	if m.mode != modeConfirm {
		return m, nil
	}
	cmd := m.confirmCmd
	m.mode = modeList
	m.confirmCmd = nil
	if msg.confirmed {
		return m, cmd
	}
	return m, nil
}

// enterConfig opens the in-cockpit settings panel over the session list, seeded
// with a working copy of the current config. Saving (ctrl+s) persists and applies
// it live; cancelling (esc) discards the edits.
func (m Model) enterConfig() (tea.Model, tea.Cmd) {
	m.editor = newConfigEditor(m.theme, m.cfg, m.pickerWidth(), m.configRows())
	m.mode = modeConfig
	m.err = nil
	m.status = ""
	return m, textinput.Blink
}

// updateConfig routes input to the open settings panel. The panel reports a
// committed field as a cfgAppliedMsg and a dismissal as a cfgClosedMsg, both
// handled by the top-level Update.
func (m Model) updateConfig(msg tea.Msg) (tea.Model, tea.Cmd) {
	editor, cmd := m.editor.update(msg)
	m.editor = editor
	return m, cmd
}

// applyConfigEvent handles the settings panel's reported outcome. A committed
// field is validated, persisted and applied to the running cockpit in place, so
// an edit takes effect and survives a restart with no separate save step; a
// commit that fails validation keeps the panel open with the error. A
// dismissal returns to the list — by then every committed edit is already
// saved. It is a no-op outside config mode.
func (m Model) applyConfigEvent(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.mode != modeConfig {
		return m, nil
	}
	switch msg := msg.(type) {
	case cfgAppliedMsg:
		return m.applyConfig(msg.cfg)
	case cfgClosedMsg:
		m.mode = modeList
		return m, m.tabs.retargetPreview(m.selectedSession())
	}
	return m, nil
}

// applyConfig persists cfg to $WASA_HOME and applies it to the running cockpit:
// the theme is re-applied, the keymap rebuilt, and the layout is picked up at the
// next render. The panel stays open so editing continues. A persist that fails
// validation or the write leaves the edits in place and shows the error on the
// panel rather than writing a bad file.
func (m Model) applyConfig(cfg config.Config) (tea.Model, tea.Cmd) {
	if err := config.Save(m.home, cfg); err != nil {
		m.editor.err = err.Error()
		return m, nil
	}
	cfg.Path = config.Path(m.home)
	m.cfg = cfg
	m.theme = newTheme(cfg.Theme)
	m.help = newMenuHelp(m.theme)
	m.keys = newKeymap(cfg.Keys)
	m.notify = makeNotifier(cfg.Notify)
	m.err = nil
	m.status = "config saved"
	return m, nil
}

// configRows is how many rows the settings panel may show; the editor scrolls its
// field list within this height.
func (m Model) configRows() int {
	return max(m.height-8, 5)
}

// enterCreate opens the create form. With a current workspace the form is seeded
// with that workspace's profiles; with no workspace — wasa launched outside any
// git repository — it opens with no profiles. The Directory field always starts
// empty rather than pre-filled with a path, so it never looks like a remembered
// value; an empty directory on submit means a plain session in the current
// working directory, and the directory browser (ctrl+f) fills it otherwise. ws
// being nil is the no-repo path, not an error.
func (m Model) enterCreate() (tea.Model, tea.Cmd) {
	ws := m.currentWorkspace()

	var names []string
	if ws != nil {
		names = make([]string, len(ws.Profiles))
		for i, p := range ws.Profiles {
			names[i] = p.Name
		}
	}

	m.form = newCreateForm(m.theme, names)
	m.mode = modeCreate
	m.err = nil
	m.status = ""
	return m, textinput.Blink
}

// attach hands the terminal to the selected session's tmux through
// tea.ExecProcess. This MUST go through tea.ExecProcess: it tells Bubble Tea to
// suspend its renderer and release the terminal so tmux owns stdin/stdout/stderr
// for the attach, then resume the TUI when tmux exits on detach. A hand-wired
// exec.Command run from inside the live program would fight Bubble Tea for the
// terminal and corrupt the display.
func (m Model) attach() (tea.Model, tea.Cmd) {
	s := m.selectedSession()
	if s == nil {
		return m, nil
	}
	if m.tabs.active == paneTerminal {
		return m.attachTerm(s)
	}
	if s.Status != registry.StatusRunning {
		m.status = "session has exited; nothing to attach to"
		return m, nil
	}

	cmd, err := m.tmux.AttachCmd(s.TmuxName)
	if err != nil {
		m.err = err
		return m, nil
	}

	sessionID := s.ID
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return attachedMsg{sessionID: sessionID, err: err}
	})
}

// attachTerm hands the terminal to the selected session's companion shell,
// spawning it first if it does not yet exist. Like the agent attach it goes
// through tea.ExecProcess so Bubble Tea releases the terminal for the duration
// and resumes on detach (C-b d). The companion is independent of the agent, so
// it attaches even when the agent session itself has exited.
func (m Model) attachTerm(s *registry.Session) (tea.Model, tea.Cmd) {
	name, err := m.tabs.terminal.prepareAttach(s)
	if err != nil {
		m.err = err
		return m, nil
	}

	cmd, err := m.tmux.AttachCmd(name)
	if err != nil {
		m.err = err
		return m, nil
	}

	sessionID := s.ID
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return attachedMsg{sessionID: sessionID, err: err}
	})
}

func (m Model) createCmd(ws *registry.Workspace, params launch.Params) tea.Cmd {
	home, reg := m.home, m.reg
	return func() tea.Msg {
		s, err := launch.CreateSession(home, reg, ws, params)
		if err != nil {
			return createdMsg{err: err}
		}
		if err := reg.Save(); err != nil {
			return createdMsg{err: err}
		}
		return createdMsg{session: s}
	}
}

func (m Model) killCmd(s *registry.Session) tea.Cmd {
	reg := m.reg
	be := m.tmux
	term := companionName(s)
	return func() tea.Msg {
		if err := launch.KillSession(reg, s); err != nil {
			return killedMsg{err: err}
		}
		_ = be.Kill(term)
		if err := reg.Save(); err != nil {
			return killedMsg{err: err}
		}
		return killedMsg{}
	}
}

func (m Model) deleteCmd(s *registry.Session) tea.Cmd {
	reg, home := m.reg, m.home
	be := m.tmux
	term := companionName(s)
	return func() tea.Msg {
		if err := launch.DeleteSession(reg, s); err != nil {
			return deletedMsg{err: err}
		}
		_ = be.Kill(term)
		_ = sessionstatus.Remove(home, s.ID)
		if err := reg.Save(); err != nil {
			return deletedMsg{err: err}
		}
		return deletedMsg{}
	}
}

// refresh re-reads the most-recently-used workspaces and clamps the cursor. It
// preserves the active workspace by id, falling back to the first workspace when
// the active one has gone away. It returns a command to re-target the preview
// stream at whatever session ends up selected.
func (m *Model) refresh() tea.Cmd {
	m.workspaces = m.reg.ListWorkspaces()
	if !m.hasWorkspace(m.activeID) {
		m.activeID = ""
		if len(m.workspaces) > 0 {
			m.activeID = m.workspaces[0].ID
		}
		m.cursor = 0
	}
	if n := len(m.sessions()); m.cursor >= n {
		m.cursor = n - 1
	}
	m.cursor = max(m.cursor, 0)
	return m.tabs.retargetPreview(m.selectedSession())
}

// rightPaneSize is the inner width and height of the right pane's body, below
// the tab strip — the area the Preview, Diff and Terminal tabs render into. It
// mirrors the sizing listView applies to the pane.
func (m Model) rightPaneSize() (w, h int) {
	bodyH := max(m.height-chromeRows, 3)
	return m.width - m.listColWidth() - 4, max(bodyH-(tabRowRows-1), 1)
}

// sweepStatuses refreshes the derived runtime status of every running session
// and fires a notification when a non-focused session transitions into a state
// that needs the user — waiting for input — or finishes — exited. Liveness comes
// from a tmux reconcile, the source of truth, which is persisted; the
// working/waiting/idle split is layered on top from each session's live pane
// content and is never persisted, so a mis-derivation can only mislabel a dot,
// never corrupt the registry. It runs on the preview tick.
func (m *Model) sweepStatuses() {
	wasRunning := make(map[string]bool)
	for _, s := range m.reg.ListSessions() {
		if s.Status == registry.StatusRunning {
			wasRunning[s.ID] = true
		}
	}
	if m.reg.Reconcile(m.tmux.Has) {
		_ = m.reg.Save()
	}

	focused := m.focusedSessionID()
	keep := make(map[string]bool)
	for _, s := range m.reg.ListSessions() {
		keep[s.ID] = true
		if s.Status != registry.StatusRunning {
			if wasRunning[s.ID] {
				m.transition(s, sessionstatus.Exited, focused)
			}
			continue
		}
		scraped := m.statuses.Observe(s.ID, m.contentFor(s))
		eff := sessionstatus.Derive(m.home, s.ID, scraped, m.now())
		m.transition(s, eff, focused)
	}
	m.statuses.Forget(keep)
	for id := range m.lastStatus {
		if !keep[id] {
			delete(m.lastStatus, id)
		}
	}
}

// transition records a session's current effective status and fires a
// notification when it has just changed into a state that needs the user —
// waiting or exited — for a session that is not focused. The first status seen
// for a session is recorded without notifying, so a session already waiting
// when the cockpit opens does not announce itself.
func (m *Model) transition(
	s *registry.Session, cur sessionstatus.Status, focusedID string,
) {
	prev, seen := m.lastStatus[s.ID]
	m.lastStatus[s.ID] = cur
	if !seen || cur == prev {
		return
	}
	m.maybeNotify(s, cur, focusedID)
}

// contentFor returns the pane content sweepStatuses derives a session's status
// from. The focused session is already streamed into the preview pane, so its
// live capture is reused rather than re-captured; every other running session is
// captured one-shot. A capture error yields empty content, which reads as idle.
func (m *Model) contentFor(s *registry.Session) string {
	if content, ok := m.tabs.liveContent(s.TmuxName); ok {
		return content
	}
	out, _ := m.tmux.Capture(s.TmuxName)
	return out
}

// maybeNotify fires a notification for a session that has just transitioned into
// waiting or exited. The caller (transition) has already established that this
// is a real change; maybeNotify stays silent in off mode, for the focused
// session (the user is already looking at it), and for a repeat within the
// debounce window so a flapping session cannot produce a burst.
func (m *Model) maybeNotify(
	s *registry.Session, cur sessionstatus.Status, focusedID string,
) {
	if m.cfg.Notify == config.NotifyOff {
		return
	}
	if cur != sessionstatus.Waiting && cur != sessionstatus.Exited {
		return
	}
	if s.ID == focusedID {
		return
	}
	now := m.now()
	if last, ok := m.lastNotifyAt[s.ID]; ok && now.Sub(last) < notifyDebounce {
		return
	}
	m.lastNotifyAt[s.ID] = now
	title, _ := sessionLabel(s)
	m.notify(notifyTitle(cur), notifyBody(title, cur))
}

func notifyTitle(s sessionstatus.Status) string {
	if s == sessionstatus.Exited {
		return "wasa: session exited"
	}
	return "wasa: session waiting"
}

func notifyBody(title string, s sessionstatus.Status) string {
	if s == sessionstatus.Exited {
		return title + " has exited"
	}
	return title + " is waiting for input"
}

// focusedSessionID is the session the user is currently looking at — the
// selected, previewed one — or "" when none is selected. Notifications are
// suppressed for it.
func (m Model) focusedSessionID() string {
	if s := m.selectedSession(); s != nil {
		return s.ID
	}
	return ""
}

// runtimeStatus is the status the list renders for a session: exited when the
// registry says so (the persisted source of truth), otherwise the derived
// runtime status, falling back to working for a running session not yet
// observed.
func (m Model) runtimeStatus(s *registry.Session) sessionstatus.Status {
	if s.Status != registry.StatusRunning {
		return sessionstatus.Exited
	}
	if st, ok := m.lastStatus[s.ID]; ok {
		return st
	}
	return sessionstatus.Working
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
