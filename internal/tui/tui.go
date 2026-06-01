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

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/joakimcarlsson/wasa/internal/backend"
	"github.com/joakimcarlsson/wasa/internal/config"
	"github.com/joakimcarlsson/wasa/internal/launch"
	"github.com/joakimcarlsson/wasa/internal/registry"
	"github.com/joakimcarlsson/wasa/internal/repo"
	"github.com/joakimcarlsson/wasa/internal/sessionstatus"
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

// Model is the cockpit's Bubble Tea model. It holds the registry it drives, the
// most-recently-used workspaces snapshot, the active workspace (tracked by id so
// it follows the workspace when an attach or create reorders the tabs), and the
// list cursor. home is $WASA_HOME, the data directory sessions are launched
// against; osHome is the user's home directory, used only to root and abbreviate
// the directory browser — the two are distinct and must not be conflated.
//
// The live-preview fields track the selected session's output stream: watchName
// is the session the preview targets (its tmux name, or "" for none); watcher is
// the live control-mode stream for it, or nil when streaming is unavailable,
// failed or dropped, in which case the fallback tick polls Capture; watchGen
// tags the active stream so a previewMsg from a superseded stream is ignored.
type Model struct {
	home   string
	osHome string
	reg    *registry.Registry
	tmux   backend.SessionBackend
	stream backend.StreamingBackend
	cfg    config.Config
	keys   keymap

	workspaces []*registry.Workspace
	activeID   string
	cursor     int

	mode    mode
	pane    paneTab
	form    createForm
	confirm confirmDialog
	picker  dirPicker
	branch  branchPicker
	editor  configEditor

	confirmCmd tea.Cmd

	width  int
	height int

	preview string

	watcher   backend.Watcher
	watchName string
	watchGen  int

	termShown   string
	termContent string
	terms       map[string]bool

	diffVP      viewport.Model
	diffSID     string
	diffText    string
	diffAdded   int
	diffRemoved int
	diffErr     error

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
	applyTheme(cfg.Theme)
	be := backend.Default()
	m := Model{
		home:         home,
		reg:          reg,
		tmux:         be,
		cfg:          cfg,
		keys:         newKeymap(cfg.Keys),
		now:          time.Now,
		statuses:     sessionstatus.NewTracker(time.Now),
		notify:       makeNotifier(cfg.Notify),
		lastNotifyAt: make(map[string]time.Time),
		lastStatus:   make(map[string]sessionstatus.Status),
		terms:        make(map[string]bool),
	}
	m.osHome, _ = os.UserHomeDir()
	m.diffVP = newDiffViewport()
	if s, ok := be.(backend.StreamingBackend); ok {
		m.stream = s
	}
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

type tickMsg struct{}

func tick() tea.Cmd {
	return tea.Tick(
		previewInterval,
		func(time.Time) tea.Msg { return tickMsg{} },
	)
}

// previewMsg carries a fresh pane capture delivered by a control-mode stream.
// gen identifies the stream that produced it so a message from a superseded or
// closed stream is ignored; ok is false when that stream's channel closed.
type previewMsg struct {
	gen     int
	content string
	ok      bool
}

// waitPreview blocks on the stream's update channel and reports the next
// capture as a previewMsg tagged with gen. Re-issued after each delivery to keep
// consuming the stream; never runs on the Update goroutine.
func waitPreview(gen int, ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		content, ok := <-ch
		return previewMsg{gen: gen, content: content, ok: ok}
	}
}

// termMsg carries the result of ensuring and capturing a session's companion
// shell. name is the companion's tmux name (so a delivery for a session no
// longer selected is ignored), content is its pane capture, and err is set when
// the companion could not be spawned or addressed.
type termMsg struct {
	name    string
	content string
	err     error
}

// diffMsg carries the computed diff of a worktree session against its base
// commit. sessionID tags the session it was computed for, so a diff that
// arrives after the selection moved is dropped rather than shown under the wrong
// session. An empty text with no err is a clean worktree (no changes).
type diffMsg struct {
	sessionID string
	text      string
	added     int
	removed   int
	err       error
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
		m.sizeDiffViewport()
		return m, nil

	case tickMsg:
		m.sweepStatuses()
		return m, tea.Batch(tick(), m.paneTick())

	case previewMsg:
		return m, m.applyPreview(msg)

	case termMsg:
		return m, m.applyTerm(msg)

	case diffMsg:
		return m, m.applyDiff(msg)

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

	if m.pane == paneDiff {
		m.sizeDiffViewport()
		m.diffVP, _ = m.diffVP.Update(msg)
	}

	switch m.keys.action(key.String()) {
	case config.ActionQuit:
		m.closeWatcher()
		m.closeTerms()
		return m, tea.Quit
	case config.ActionTabNext:
		m.cycleTab(1)
	case config.ActionTabPrev:
		m.cycleTab(-1)
	case config.ActionPaneTab:
		m.cyclePaneTab(1)
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
// the selection or switched the active pane tab. It re-targets the preview
// stream (tearing it down off the Preview tab) and, on the Terminal tab, kicks
// an immediate companion ensure+capture so switching to it or moving the cursor
// shows the shell without waiting for the next tick.
func (m *Model) afterListChange() tea.Cmd {
	cmd := m.ensureWatcher()
	switch m.pane {
	case paneTerminal:
		cmd = tea.Batch(cmd, m.ensureTermCmd())
	case paneDiff:
		cmd = tea.Batch(cmd, m.ensureDiffCmd())
	}
	return cmd
}

func (m Model) updateCreate(msg tea.Msg) (tea.Model, tea.Cmd) {
	prevBranchRepo := m.form.branchRepo
	form, result, cmd := m.form.update(msg)
	m.form = form
	if m.form.branchRepo != prevBranchRepo {
		m.form.setProfiles(m.profilesFor(m.form.branchRepo))
	}
	switch result {
	case formCancel:
		m.mode = modeList
		return m, nil
	case formPickDir:
		return m.enterPick()
	case formPickBranch:
		return m.enterBranchPick()
	case formSubmit:
		return m.submitCreate()
	}
	return m, cmd
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
		rootPath, sel, m.osHome, m.recentDirs(),
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

// updatePick routes input for the open directory browser. Choosing a directory
// writes it into the form's Directory field and returns to the form; cancelling
// returns to the form unchanged.
func (m Model) updatePick(msg tea.Msg) (tea.Model, tea.Cmd) {
	picker, result, cmd := m.picker.update(msg)
	m.picker = picker
	switch result {
	case pickCancel:
		m.mode = modeCreate
		return m, textinput.Blink
	case pickChoose:
		m.form.setDir(picker.chosen)
		m.form.setProfiles(m.profilesFor(m.form.branchRepo))
		m.mode = modeCreate
		return m, textinput.Blink
	}
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
	m.form.syncBranchRepo()
	if !m.form.branchEnabled() {
		return m, nil
	}
	m.branch = newBranchPicker(
		repoBranches(m.form.branchRepo), m.pickerWidth(), m.pickerHeight(),
	)
	m.mode = modePickBranch
	return m, textinput.Blink
}

// updateBranchPick routes input for the open branch picker. Choosing or typing a
// branch writes it into the form's Branch field and returns to the form;
// cancelling returns unchanged.
func (m Model) updateBranchPick(msg tea.Msg) (tea.Model, tea.Cmd) {
	picker, result, cmd := m.branch.update(msg)
	m.branch = picker
	switch result {
	case pickCancel:
		m.mode = modeCreate
		return m, textinput.Blink
	case pickChoose:
		m.form.setBranch(picker.chosen)
		m.mode = modeCreate
		return m, textinput.Blink
	}
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
		fmt.Sprintf("Delete %q?\nThis cannot be undone.", title), s,
	)
	return m.enterConfirm(
		newConfirmDialog("Delete session", body, "Delete", "Cancel", true),
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
		fmt.Sprintf(
			"Kill %q?\nIt stops but stays in the list as exited.", title,
		), s,
	)
	return m.enterConfirm(
		newConfirmDialog("Kill session", body, "Kill", "Cancel", true),
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

// updateConfirm routes key input for the active confirm modal. Accepting it runs
// the stored command; cancelling returns to the list with no change.
func (m Model) updateConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	dialog, result := m.confirm.update(msg)
	m.confirm = dialog
	switch result {
	case confirmYes:
		cmd := m.confirmCmd
		m.mode = modeList
		m.confirmCmd = nil
		return m, cmd
	case confirmNo:
		m.mode = modeList
		m.confirmCmd = nil
		return m, nil
	}
	return m, nil
}

// enterConfig opens the in-cockpit settings panel over the session list, seeded
// with a working copy of the current config. Saving (ctrl+s) persists and applies
// it live; cancelling (esc) discards the edits.
func (m Model) enterConfig() (tea.Model, tea.Cmd) {
	m.editor = newConfigEditor(m.cfg, m.pickerWidth(), m.configRows())
	m.mode = modeConfig
	m.err = nil
	m.status = ""
	return m, textinput.Blink
}

// updateConfig routes input for the open settings panel. Each committed field
// (cfgApply) is validated, persisted and applied to the running cockpit in place,
// so an edit takes effect and survives a restart with no separate save step; a
// commit that fails validation keeps the panel open with the error. Closing
// (cfgClose) returns to the list — by then every committed edit is already saved.
func (m Model) updateConfig(msg tea.Msg) (tea.Model, tea.Cmd) {
	editor, result, cmd := m.editor.update(msg)
	m.editor = editor
	switch result {
	case cfgApply:
		return m.applyConfig(editor.config())
	case cfgClose:
		m.mode = modeList
		return m, m.ensureWatcher()
	}
	return m, cmd
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
	applyTheme(cfg.Theme)
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

	m.form = newCreateForm(names)
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
	if m.pane == paneTerminal {
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
	name := companionName(s)
	dir := s.WorktreePath
	if dir == "" {
		dir = s.WorkingDir
	}
	switch has, err := m.tmux.Has(name); {
	case err != nil:
		m.err = err
		return m, nil
	case !has:
		if err := m.tmux.SpawnEnv(name, dir, nil, launch.Shell()); err != nil {
			m.err = err
			return m, nil
		}
	}
	m.terms[name] = true

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
	return m.ensureWatcher()
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

// ensureWatcher makes the live preview stream track the selected session. When
// the target changed it tears down the old stream and clears the stale preview;
// when streaming is available and no stream is live for the target it opens one
// and returns the command that waits on it. It returns nil when nothing changed,
// when there is no running target, or when streaming is unavailable or fails —
// in which cases the fallback tick polls Capture instead. Never blocks on a
// capture itself.
func (m *Model) ensureWatcher() tea.Cmd {
	name := m.previewTarget()
	if name != m.watchName {
		m.closeWatcher()
		m.watchName = name
		m.preview = ""
	}
	if name == "" || m.stream == nil || m.watcher != nil {
		return nil
	}
	w, err := m.stream.Watch(name)
	if err != nil {
		return nil
	}
	m.watcher = w
	m.watchGen++
	return waitPreview(m.watchGen, w.Updates())
}

// closeWatcher tears down any live stream and invalidates in-flight previewMsgs
// by bumping the generation, so a late delivery from the closed stream is
// dropped rather than applied.
func (m *Model) closeWatcher() {
	if m.watcher != nil {
		_ = m.watcher.Close()
		m.watcher = nil
	}
	m.watchGen++
}

// applyPreview handles a previewMsg from a stream. It ignores deliveries from a
// superseded stream, drops to the fallback poll when the stream closed, and
// otherwise stores the capture and re-arms the wait on the same stream.
func (m *Model) applyPreview(msg previewMsg) tea.Cmd {
	if msg.gen != m.watchGen || m.watcher == nil {
		return nil
	}
	if !msg.ok {
		m.closeWatcher()
		return nil
	}
	m.preview = msg.content
	return waitPreview(msg.gen, m.watcher.Updates())
}

// pollOrReconnect runs on the fallback tick. With a live stream it does nothing
// (the stream delivers updates and runs its own safety captures). Otherwise it
// tries to (re)establish a stream for the selected session, and failing that
// falls back to a one-shot Capture poll — the only path on a non-streaming
// backend (Windows) and the recovery path after a dropped connection.
func (m *Model) pollOrReconnect() tea.Cmd {
	if m.watcher != nil {
		return nil
	}
	if cmd := m.ensureWatcher(); cmd != nil {
		return cmd
	}
	m.pollCapture()
	return nil
}

// pollCapture re-captures the preview target with a one-shot Capture. An empty
// target — no running selection, or the Preview tab not active — clears the
// buffer and captures nothing, so the fallback poll, like the stream, does no
// work when another tab is shown. Errors are swallowed: the preview is a
// convenience, not a source of truth. This is the fallback when no stream is
// available; on the streaming path it does not run.
func (m *Model) pollCapture() {
	name := m.previewTarget()
	if name == "" {
		m.preview = ""
		return
	}
	if out, err := m.tmux.Capture(name); err == nil {
		m.preview = out
	}
}

// paneTick is the per-tick work for the active right-pane tab. The Preview and
// Diff tabs poll-or-reconnect the preview stream — a near no-op off the Preview
// tab, since previewTarget is then empty — while the Terminal tab ensures the
// selected session's companion shell exists and re-captures it.
func (m *Model) paneTick() tea.Cmd {
	if m.pane == paneTerminal {
		return m.ensureTermCmd()
	}
	return m.pollOrReconnect()
}

// ensureTermCmd returns a command that lazily spawns the selected session's
// companion shell — a tmux session distinct from the agent's, named off its
// TmuxName, running launch.Shell() in the session's worktree (or working)
// directory — when one does not already exist, then captures it for the
// Terminal tab body. An existing companion is reused rather than respawned, so
// it survives cockpit restarts. With no session selected it clears the body.
func (m *Model) ensureTermCmd() tea.Cmd {
	s := m.selectedSession()
	if s == nil {
		return func() tea.Msg { return termMsg{} }
	}
	name := companionName(s)
	dir := s.WorktreePath
	if dir == "" {
		dir = s.WorkingDir
	}
	be := m.tmux
	return func() tea.Msg {
		has, err := be.Has(name)
		if err != nil {
			return termMsg{name: name, err: err}
		}
		if !has {
			if err := be.SpawnEnv(name, dir, nil, launch.Shell()); err != nil {
				return termMsg{name: name, err: err}
			}
		}
		out, _ := be.Capture(name)
		return termMsg{name: name, content: out}
	}
}

// applyTerm stores a companion capture for rendering and records the companion
// as live so it is torn down on exit. A delivery whose companion is no longer
// the selected session's is dropped, so a late capture cannot overwrite the
// body after the selection moved. A spawn or address error surfaces on the
// status line.
func (m *Model) applyTerm(msg termMsg) tea.Cmd {
	if msg.err != nil {
		m.err = msg.err
		return nil
	}
	if msg.name == "" {
		m.termShown = ""
		m.termContent = ""
		return nil
	}
	m.terms[msg.name] = true
	if s := m.selectedSession(); s == nil || companionName(s) != msg.name {
		return nil
	}
	m.termShown = msg.name
	m.termContent = msg.content
	return nil
}

// closeTerms kills every companion shell this run spawned. It runs on quit so no
// wasa_*_term sessions are left behind. Each kill is best-effort: a companion a
// session kill or delete already removed is gone, and tmux's error for a missing
// session is swallowed.
func (m *Model) closeTerms() {
	for name := range m.terms {
		_ = m.tmux.Kill(name)
	}
	m.terms = make(map[string]bool)
}

// companionName is the deterministic tmux name of a session's companion shell:
// its agent TmuxName with a _term suffix. Deriving it from the stable TmuxName
// keeps it identical across cockpit restarts and distinct from the agent
// session, so the two never collide.
func companionName(s *registry.Session) string {
	return s.TmuxName + "_term"
}

// newDiffViewport builds the Diff tab's scrollable viewport with a keymap that
// avoids the cockpit list bindings: it scrolls with PageUp/PageDown and the
// ctrl+f/ctrl+b/ctrl+u/ctrl+d chords and leaves the bare arrow keys to the list
// so up/down keep moving the session cursor (which re-targets the diff).
func newDiffViewport() viewport.Model {
	vp := viewport.New(0, 0)
	vp.KeyMap = viewport.KeyMap{
		PageDown:     key.NewBinding(key.WithKeys("pgdown", "ctrl+f")),
		PageUp:       key.NewBinding(key.WithKeys("pgup", "ctrl+b")),
		HalfPageDown: key.NewBinding(key.WithKeys("ctrl+d")),
		HalfPageUp:   key.NewBinding(key.WithKeys("ctrl+u")),
	}
	return vp
}

// rightPaneSize is the inner width and height of the right pane's body, below
// the tab strip — the area the Preview, Diff and Terminal tabs render into. It
// mirrors the sizing listView applies to the pane.
func (m Model) rightPaneSize() (w, h int) {
	bodyH := max(m.height-chromeRows, 3)
	return m.width - m.listColWidth() - 4, max(bodyH-(tabRowRows-1), 1)
}

// sizeDiffViewport sizes the diff viewport to the pane body minus the summary
// line, so its paging math and render match what diffBody draws. It runs on
// resize and whenever a diff is loaded, never per tick.
func (m *Model) sizeDiffViewport() {
	w, h := m.rightPaneSize()
	m.diffVP.Width = max(w, 1)
	m.diffVP.Height = max(h-1, 1)
}

// ensureDiffCmd returns a command that computes the selected worktree session's
// diff against its recorded base commit, when that diff is not already loaded.
// It is a no-op (nil) when the diff for the selection is already shown, so it
// fires only on a selection change or a switch to the Diff tab, never per tick.
// A plain (non-worktree) session loads an empty diff; diffBody renders the
// explanatory state for it from the session's own fields.
func (m *Model) ensureDiffCmd() tea.Cmd {
	s := m.selectedSession()
	if s == nil || m.diffSID == s.ID {
		return nil
	}
	sid := s.ID
	branch, wt, base := s.Branch, s.WorktreePath, s.BaseCommit
	if branch == "" || wt == "" || base == "" {
		return func() tea.Msg { return diffMsg{sessionID: sid} }
	}
	ws, ok := m.reg.Workspace(s.WorkspaceID)
	if !ok {
		return func() tea.Msg {
			return diffMsg{
				sessionID: sid,
				err:       fmt.Errorf("workspace not found"),
			}
		}
	}
	repo, home, wsID := ws.RepoPath, m.home, s.WorkspaceID
	return func() tea.Msg {
		res, err := worktree.New(repo, home, wsID).Diff(wt, base)
		if err != nil {
			return diffMsg{sessionID: sid, err: err}
		}
		return diffMsg{
			sessionID: sid, text: res.Text,
			added: res.Added, removed: res.Removed,
		}
	}
}

// applyDiff stores a computed diff for rendering, dropping a delivery whose
// session is no longer selected so a slow diff cannot overwrite the body after
// the cursor moved. It loads the colorized content into the viewport and scrolls
// it back to the top for the new session.
func (m *Model) applyDiff(msg diffMsg) tea.Cmd {
	if s := m.selectedSession(); s == nil || s.ID != msg.sessionID {
		return nil
	}
	m.diffSID = msg.sessionID
	m.diffErr = msg.err
	m.diffText = msg.text
	m.diffAdded, m.diffRemoved = msg.added, msg.removed
	m.sizeDiffViewport()
	m.diffVP.SetContent(colorizeDiff(msg.text))
	m.diffVP.SetYOffset(0)
	return nil
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
// from. The focused session is already streamed into m.preview, so its live
// capture is reused rather than re-captured; every other running session is
// captured one-shot. A capture error yields empty content, which reads as idle.
func (m *Model) contentFor(s *registry.Session) string {
	if s.TmuxName == m.watchName && m.watcher != nil {
		return m.preview
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
