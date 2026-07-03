package tui

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/joakimcarlsson/wasa-cli/internal/backend"
	"github.com/joakimcarlsson/wasa-cli/internal/config"
	"github.com/joakimcarlsson/wasa-cli/internal/launch"
	"github.com/joakimcarlsson/wasa-cli/internal/registry"
	"github.com/joakimcarlsson/wasa-cli/internal/repo"
	"github.com/joakimcarlsson/wasa-cli/internal/sessionstatus"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/component"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/modal"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/pane"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/theme"
	"github.com/joakimcarlsson/wasa-cli/internal/worktree"
)

// mode is the model's interaction mode: browsing the session list or filling in
// the create form.
type mode int

const (
	modeList mode = iota
	modeCreate
	modeConfirm
	modePick
	modePickWorkspace
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
// The three right-pane feature machines — preview, diff and terminal — own their
// own state and lifecycle (see internal/tui/pane). The Model holds them through
// the pane.Tabbed container, which also tracks the active tab: the Model targets
// each at the selected session, routes the typed messages back, and reaches the
// panes through m.tabbed to render the active tab's body.
type Model struct {
	home   string
	osHome string
	reg    *registry.Registry
	tmux   backend.SessionBackend
	stream backend.StreamingBackend
	cfg    config.Config
	keys   component.Keymap
	theme  theme.Theme

	workspaces []*registry.Workspace
	activeID   string
	cursor     int

	mode    mode
	form    modal.CreateForm
	confirm modal.ConfirmDialog
	picker  component.DirectoryPicker
	branch  component.BranchPicker
	editor  modal.ConfigEditor
	filter  filterState

	confirmCmd tea.Cmd

	width  int
	height int

	tabbed pane.Tabbed

	now          func() time.Time
	statuses     *sessionstatus.Tracker
	notify       func(title, body string)
	lastNotifyAt map[string]time.Time
	lastStatus   map[string]sessionstatus.Status
	churn        map[string]churnStat

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
	th := theme.NewTheme(cfg.Theme)
	m := Model{
		home:         home,
		reg:          reg,
		tmux:         be,
		cfg:          cfg,
		keys:         component.NewKeymap(cfg.Keys),
		theme:        th,
		now:          time.Now,
		statuses:     sessionstatus.NewTracker(time.Now),
		notify:       makeNotifier(cfg.Notify),
		lastNotifyAt: make(map[string]time.Time),
		lastStatus:   make(map[string]sessionstatus.Status),
		churn:        make(map[string]churnStat),
	}
	m.osHome, _ = os.UserHomeDir()
	if s, ok := be.(backend.StreamingBackend); ok {
		m.stream = s
	}
	m.tabbed = pane.NewTabbed(m.stream, be, th)
	m.workspaces = reg.ListWorkspaces()
	switch {
	case currentID != "" && m.hasWorkspace(currentID):
		m.activeID = currentID
	case len(m.workspaces) > 0:
		m.activeID = m.workspaces[0].ID
	}
	return m
}

// NewProgram builds the cockpit's Bubble Tea program without running it, so
// the caller can hand its Send to collaborators — the link layer marshals
// dispatch work onto the program — before blocking in Run. It uses the
// alternate screen so the terminal is restored on exit and around every
// attach.
func NewProgram(
	home string,
	reg *registry.Registry,
	currentID string,
	cfg config.Config,
) *tea.Program {
	return tea.NewProgram(
		New(home, reg, currentID, cfg), tea.WithAltScreen(),
	)
}

// Run launches the cockpit over reg and blocks until the user quits. cfg is
// the resolved cockpit configuration.
func Run(
	home string,
	reg *registry.Registry,
	currentID string,
	cfg config.Config,
) error {
	_, err := NewProgram(home, reg, currentID, cfg).Run()
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

// churnInterval is the cadence of the diff-refresh tick: the selected worktree
// session's full diff is recomputed, and every worktree session's +N/−M churn
// stat is refreshed, so the pane and the list rows track the agent's edits
// without the user re-selecting the row. It is deliberately slower than the
// preview tick — numstat is cheap but still shells out to git per session — and
// is a no-op (no git) when there are no worktree sessions.
const churnInterval = time.Second

// Init implements tea.Model. It fires one immediate tick of each loop so the
// preview opens its stream (or polls) and the churn stats populate right away
// rather than after the first interval.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		func() tea.Msg { return tickMsg{} },
		func() tea.Msg { return churnTickMsg{} },
	)
}

type tickMsg struct{}

func tick() tea.Cmd {
	return tea.Tick(
		previewInterval,
		func(time.Time) tea.Msg { return tickMsg{} },
	)
}

// churnTickMsg fires the diff-refresh loop; churnMsg carries the computed
// per-session churn back to the model. They are distinct so the timer re-arms
// and issues the git work in one step while the result lands in another.
type churnTickMsg struct{}

type churnMsg struct {
	stats map[string]churnStat
}

func churnTick() tea.Cmd {
	return tea.Tick(
		churnInterval,
		func(time.Time) tea.Msg { return churnTickMsg{} },
	)
}

type createdMsg struct {
	session *registry.Session
	err     error
}

type killedMsg struct{ err error }

type deletedMsg struct{ err error }

type workspaceDeletedMsg struct {
	name string
	err  error
}

// workspaceAddedMsg carries the outcome of git-initialising a directory and
// registering it as a workspace (the confirm-gated path in initWorkspaceCmd) back
// to the update loop. created distinguishes a freshly registered workspace from
// one the init turned out to already cover.
type workspaceAddedMsg struct {
	wsID    string
	name    string
	created bool
	err     error
}

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

	case churnTickMsg:
		return m, tea.Batch(churnTick(), m.churnCmd(), m.refreshDiffCmd())

	case churnMsg:
		m.applyChurn(msg)
		return m, nil

	case pane.PreviewMsg:
		return m, m.tabbed.Preview.Apply(msg)

	case pane.TermMsg:
		return m, m.applyTerm(msg)

	case pane.DiffMsg:
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

	case workspaceDeletedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.status = "deleted workspace " + msg.name
		return m, m.refresh()

	case workspaceAddedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, m.afterListChange()
		}
		m.err = nil
		if msg.created {
			m.status = "initialized and added workspace " + msg.name
		} else {
			m.status = "workspace " + msg.name + " already added"
		}
		m.activeID = msg.wsID
		m.cursor = 0
		return m, m.refresh()

	case DispatchMsg:
		return m.handleDispatch(msg)

	case SessionTargetMsg:
		m.resolveSessionTarget(msg)
		return m, nil

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

	case component.FilterTickMsg:
		if m.mode == modePick || m.mode == modePickWorkspace {
			return m, m.picker.TickFilter(msg.Gen)
		}
		return m, nil

	case component.FilterResultMsg:
		if m.mode == modePick || m.mode == modePickWorkspace {
			m.picker.ApplyFilterResult(msg)
		}
		return m, nil

	case modal.ConfirmAcceptedMsg:
		cmd := m.confirmCmd
		m.mode = modeList
		m.confirmCmd = nil
		return m, cmd

	case modal.ConfirmCancelledMsg:
		m.mode = modeList
		m.confirmCmd = nil
		return m, nil

	case modal.FormSubmitMsg:
		return m.submitCreate()

	case modal.FormCancelMsg:
		m.mode = modeList
		return m, nil

	case modal.FormPickDirMsg:
		return m.enterPick()

	case modal.FormPickBranchMsg:
		return m.enterBranchPick()

	case component.DirChosenMsg:
		if m.mode == modePickWorkspace {
			return m.addWorkspace(msg.Path)
		}
		m.form.SetDir(msg.Path)
		m.form.SetProfiles(m.profilesFor(m.form.BranchRepo))
		m.mode = modeCreate
		return m, textinput.Blink

	case component.DirCancelledMsg:
		if m.mode == modePickWorkspace {
			m.mode = modeList
			return m, m.afterListChange()
		}
		m.mode = modeCreate
		return m, textinput.Blink

	case component.BranchChosenMsg:
		m.form.SetBranch(msg.Branch)
		m.mode = modeCreate
		return m, textinput.Blink

	case component.BranchCancelledMsg:
		m.mode = modeCreate
		return m, textinput.Blink

	case modal.ConfigApplyMsg:
		return m.applyConfig(m.editor.Config())

	case modal.ConfigCloseMsg:
		m.mode = modeList
		return m, m.tabbed.Preview.SetTarget(m.previewTarget())
	}

	switch m.mode {
	case modeCreate:
		return m.updateCreate(msg)
	case modeConfirm:
		return m.updateConfirm(msg)
	case modePick, modePickWorkspace:
		return m.updatePick(msg)
	case modePickBranch:
		return m.updateBranchPick(msg)
	case modeConfig:
		return m.updateConfig(msg)
	}
	return m.updateList(msg)
}

func (m Model) updateList(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.filter.active {
		return m.updateFilter(msg)
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	if m.tabbed.Active() == pane.TabDiff {
		m.sizeDiffViewport()
		m.tabbed.Diff.Update(msg)
	}

	switch m.keys.Action(key.String()) {
	case config.ActionQuit:
		m.tabbed.Preview.Close()
		m.tabbed.Terminal.Close(m.tmux)
		return m, tea.Quit
	case config.ActionTabNext:
		m.cycleTab(1)
	case config.ActionTabPrev:
		m.cycleTab(-1)
	case config.ActionPaneTab:
		m.tabbed.Cycle(1)
	case config.ActionCursorUp:
		if m.cursor > 0 {
			m.cursor--
		}
	case config.ActionCursorDown:
		if m.cursor < len(m.sessions())-1 {
			m.cursor++
		}
	case config.ActionFilter:
		return m.enterFilter()
	case config.ActionWorkspaceAdd:
		return m.enterWorkspaceAdd()
	case config.ActionWorkspaceDelete:
		return m.enterWorkspaceDelete()
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

// updateCreate routes input to the create form, re-deriving the profile menu
// when the chosen directory's repository changes, and forwards the command that
// carries the form's decision back to the top-level Update.
func (m Model) updateCreate(msg tea.Msg) (tea.Model, tea.Cmd) {
	prevBranchRepo := m.form.BranchRepo
	var cmd tea.Cmd
	m.form, cmd = m.form.Update(msg)
	if m.form.BranchRepo != prevBranchRepo {
		m.form.SetProfiles(m.profilesFor(m.form.BranchRepo))
	}
	return m, cmd
}

// submitCreate turns the create form into a session. Inside a workspace the
// session belongs to the active workspace: a worktree session's branch is created
// against its repository and a plain session runs in that repository's root, both
// already carried in the form's params, so the active tab is used as-is. On the
// orphan tab there is no workspace anchor: a worktree session is created against
// the repository of the folder picked in the form — registered (and reg persisted)
// when not yet known, with the profile constrained to that workspace's profiles —
// and a plain session runs in the picked folder, defaulting to the current working
// directory when none was given.
func (m Model) submitCreate() (tea.Model, tea.Cmd) {
	ws := m.currentWorkspace()
	params := m.form.Params()
	if params.Branch != "" {
		if ws == nil {
			target, err := m.worktreeWorkspace()
			if err != nil {
				m.err = err
				m.mode = modeList
				return m, nil
			}
			ws = target
		}
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

// worktreeWorkspace resolves the workspace an orphan-tab worktree session is
// created against: the repository of the folder picked in the form, derived from
// the same branchRepo the Branch field already resolved. It registers that
// repository's workspace when it is not yet known so the session lands in the
// picked repo even when wasa was launched elsewhere; createCmd persists reg
// afterwards. It is only reached outside a workspace — inside one the active
// workspace is used directly.
func (m Model) worktreeWorkspace() (*registry.Workspace, error) {
	repoPath, remoteURL, err := repo.Resolve(m.form.BranchRepo)
	if err != nil {
		return nil, err
	}
	ws, _ := repo.Register(m.reg, repoPath, remoteURL)
	return ws, nil
}

// enterWorkspaceAdd opens the directory browser to register a repository as a
// workspace tab, without creating a session. Unlike the create-form picker it
// floats over the cockpit list (modePickWorkspace). It has no Directory field to
// seed it, so it opens at pickerRoot — the working directory wasa was launched
// in — from which the user ascends ("-") or filters to the repo to add.
func (m Model) enterWorkspaceAdd() (tea.Model, tea.Cmd) {
	m.picker = component.NewDirectoryPicker(
		m.theme, m.pickerRoot(), "", m.osHome, m.recentDirs(),
		m.pickerWidth(), m.pickerHeight(),
	)
	m.mode = modePickWorkspace
	m.err = nil
	m.status = ""
	return m, textinput.Blink
}

// addWorkspace registers the git repository at path as a workspace and makes it
// the active tab, resetting the cursor to the top of its (likely empty) session
// list. It routes through the same repo.Resolve + repo.Register path the CLI and
// auto-registration use, so the workspace lands under the same content-addressed
// id: picking an already-registered repository activates its existing tab rather
// than duplicating it, and reg is persisted only when a workspace was newly
// created. A directory that is not yet a git repository is not rejected outright:
// it routes to a confirm that offers to git-init it first, so a new or
// not-yet-versioned project can be turned into a workspace without dropping to a
// shell.
func (m Model) addWorkspace(path string) (tea.Model, tea.Cmd) {
	repoPath, remoteURL, err := repo.Resolve(path)
	if err != nil {
		return m.confirmInitWorkspace(path)
	}
	m.mode = modeList
	ws, created := repo.Register(m.reg, repoPath, remoteURL)
	if created {
		if err := m.reg.Save(); err != nil {
			m.err = err
			return m, m.afterListChange()
		}
		m.status = "added workspace " + ws.Name
	} else {
		m.status = "workspace " + ws.Name + " already added"
	}
	m.err = nil
	m.activeID = ws.ID
	m.cursor = 0
	return m, m.refresh()
}

// confirmInitWorkspace opens a confirm dialog offering to git-init the directory
// at path before registering it as a workspace, reached when path is not yet a git
// repository. It guards that path is an existing directory first: a non-directory
// (or a path that has gone away) is surfaced as an error rather than offered an
// init that could not make sense, so only a real folder is ever proposed for
// initialization. Confirming runs initWorkspaceCmd; cancelling returns to the list
// untouched.
func (m Model) confirmInitWorkspace(path string) (tea.Model, tea.Cmd) {
	info, statErr := os.Stat(path)
	if statErr != nil || !info.IsDir() {
		m.mode = modeList
		m.err = fmt.Errorf("%s is not a git repository", path)
		return m, m.afterListChange()
	}
	body := fmt.Sprintf(
		"%s is not a git repository.\n\n", component.HomeRel(path, m.osHome),
	) +
		"Initialize a git repository here and add it as a workspace? " +
		"This creates a .git directory in that folder; nothing else on disk " +
		"is touched."
	return m.enterConfirm(
		modal.NewConfirmDialog(
			m.theme,
			"Initialize repository",
			body,
			"Initialize",
			"Cancel",
			false,
		),
		m.initWorkspaceCmd(path),
	)
}

// initWorkspaceCmd git-inits the directory at path and registers it as a
// workspace, returning a workspaceAddedMsg with the outcome. It runs off the
// update loop because it shells out to git; the model is mutated when the message
// lands. reg is persisted only when a new workspace was created, mirroring
// addWorkspace.
func (m Model) initWorkspaceCmd(path string) tea.Cmd {
	reg := m.reg
	return func() tea.Msg {
		if err := repo.Init(path); err != nil {
			return workspaceAddedMsg{err: err}
		}
		repoPath, remoteURL, err := repo.Resolve(path)
		if err != nil {
			return workspaceAddedMsg{err: err}
		}
		ws, created := repo.Register(reg, repoPath, remoteURL)
		if created {
			if err := reg.Save(); err != nil {
				return workspaceAddedMsg{err: err}
			}
		}
		return workspaceAddedMsg{wsID: ws.ID, name: ws.Name, created: created}
	}
}

// enterWorkspaceDelete opens the delete-confirmation modal for the active
// workspace. Deleting a workspace cascades: every session it owns is torn down —
// tmux stopped, worktree removed, branch and any uncommitted work discarded —
// before the workspace tab is removed, so the confirm spells out how many
// sessions go and that the work on their branches is gone. The repository on disk
// is never touched. With no active workspace it is a no-op.
func (m Model) enterWorkspaceDelete() (tea.Model, tea.Cmd) {
	ws := m.currentWorkspace()
	if ws == nil {
		return m, nil
	}
	body := fmt.Sprintf("Delete workspace %q?\n", ws.Name) +
		workspaceDeleteWarning(len(m.workspaceSessions()))
	return m.enterConfirm(
		modal.NewConfirmDialog(
			m.theme, "Delete workspace", body, "Delete", "Cancel", true,
		),
		m.workspaceDeleteCmd(ws),
	)
}

// workspaceDeleteWarning is the confirm-body line describing what deleting a
// workspace with n sessions does, made specific so the user sees the blast radius
// before confirming.
func workspaceDeleteWarning(n int) string {
	switch n {
	case 0:
		return "It has no sessions. The repository on disk is not touched."
	case 1:
		return "This tears down its 1 session — removing the worktree and " +
			"discarding the branch and any uncommitted work on it. " +
			"The repository on disk is not touched."
	default:
		return fmt.Sprintf(
			"This tears down all %d sessions — removing their worktrees and "+
				"discarding their branches and any uncommitted work on them. "+
				"The repository on disk is not touched.", n,
		)
	}
}

// workspaceDeleteCmd tears down ws and removes its tab. It captures ws's
// companion shell names up front so the bulk teardown can kill them too — they
// are a cockpit artifact the shared launch.DeleteWorkspace path does not know
// about — then runs the cascade, kills the companions, and persists reg.
func (m Model) workspaceDeleteCmd(ws *registry.Workspace) tea.Cmd {
	reg, home, be := m.reg, m.home, m.tmux
	var companions []string
	for _, s := range m.workspaceSessions() {
		companions = append(companions, companionName(s.TmuxName))
	}
	name := ws.Name
	return func() tea.Msg {
		if _, err := launch.DeleteWorkspace(reg, be, home, ws); err != nil {
			return workspaceDeletedMsg{err: err}
		}
		for _, c := range companions {
			_ = be.Kill(c)
		}
		if err := reg.Save(); err != nil {
			return workspaceDeletedMsg{err: err}
		}
		return workspaceDeletedMsg{name: name}
	}
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
		modal.NewConfirmDialog(
			m.theme, "Delete session", body, "Delete", "Cancel", true,
		),
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
		modal.NewConfirmDialog(
			m.theme, "Kill session", body, "Kill", "Cancel", true,
		),
		m.killCmd(s),
	)
}

// enterConfirm opens dialog as a modal and stores onConfirm as the command to
// run if it is accepted.
func (m Model) enterConfirm(
	dialog modal.ConfirmDialog,
	onConfirm tea.Cmd,
) (tea.Model, tea.Cmd) {
	m.confirm = dialog
	m.confirmCmd = onConfirm
	m.mode = modeConfirm
	m.err = nil
	m.status = ""
	return m, nil
}

// updateConfirm routes key input to the active confirm modal, forwarding the
// command that carries its decision back to the top-level Update.
func (m Model) updateConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.confirm, cmd = m.confirm.Update(msg)
	return m, cmd
}

// enterConfig opens the in-cockpit settings panel over the session list, seeded
// with a working copy of the current config. Saving (ctrl+s) persists and applies
// it live; cancelling (esc) discards the edits.
func (m Model) enterConfig() (tea.Model, tea.Cmd) {
	m.editor = modal.NewConfigEditor(
		m.theme, m.cfg, m.pickerWidth(), m.configRows(),
	)
	m.mode = modeConfig
	m.err = nil
	m.status = ""
	return m, textinput.Blink
}

// updateConfig routes input to the open settings panel, forwarding the command
// that carries its decision (apply or close) back to the top-level Update.
func (m Model) updateConfig(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.editor, cmd = m.editor.Update(msg)
	return m, cmd
}

// applyConfig persists cfg to $WASA_HOME and applies it to the running cockpit:
// the theme is re-applied, the keymap rebuilt, and the layout is picked up at the
// next render. The panel stays open so editing continues. A persist that fails
// validation or the write leaves the edits in place and shows the error on the
// panel rather than writing a bad file.
func (m Model) applyConfig(cfg config.Config) (tea.Model, tea.Cmd) {
	if err := config.Save(m.home, cfg); err != nil {
		m.editor.SetErr(err.Error())
		return m, nil
	}
	cfg.Path = config.Path(m.home)
	m.cfg = cfg
	m.theme = theme.NewTheme(cfg.Theme)
	m.tabbed.Diff.SetTheme(m.theme)
	m.keys = component.NewKeymap(cfg.Keys)
	m.notify = makeNotifier(cfg.Notify)
	m.err = nil
	m.status = "config saved"
	return m, nil
}

// enterCreate opens the create form. With a current workspace the form is seeded
// with that workspace's profiles and its repository root, which anchors the
// session and drops the Directory field — a session created inside a workspace
// belongs to that workspace's repo and is never pointed at a free-form path. With
// no workspace — wasa launched outside any git repository, or the orphan tab — the
// Directory field returns and is the session's only anchor: it starts empty, and
// an empty directory on submit means a plain session in the current working
// directory, with the directory browser (ctrl+f) filling it otherwise. ws being
// nil is the orphan path, not an error.
func (m Model) enterCreate() (tea.Model, tea.Cmd) {
	ws := m.currentWorkspace()

	var (
		names    []string
		repoPath string
	)
	if ws != nil {
		names = make([]string, len(ws.Profiles))
		for i, p := range ws.Profiles {
			names[i] = p.Name
		}
		repoPath = ws.RepoPath
	}

	m.form = modal.NewCreateForm(m.theme, names, repoPath)
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
	if m.tabbed.Active() == pane.TabTerminal {
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
	cmd, err := m.tabbed.Terminal.AttachCmd(s.TmuxName, sessionDir(s), m.tmux)
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
	term := companionName(s.TmuxName)
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
	term := companionName(s.TmuxName)
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
// preserves the active tab by id — a workspace or the synthetic orphan tab —
// falling back to the first tab when the active one has gone away. It returns a
// command to re-target the preview stream at whatever session ends up selected.
func (m *Model) refresh() tea.Cmd {
	m.workspaces = m.reg.ListWorkspaces()
	if !m.hasTab(m.activeID) {
		m.activeID = ""
		if tabs := m.tabList(); len(tabs) > 0 {
			m.activeID = tabs[0].id
		}
		m.cursor = 0
	}
	if n := len(m.sessions()); m.cursor >= n {
		m.cursor = n - 1
	}
	m.cursor = max(m.cursor, 0)
	return m.tabbed.Preview.SetTarget(m.previewTarget())
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
// live capture is reused rather than re-captured; every other running session
// is captured one-shot. A capture error yields empty content, which reads as
// idle.
func (m *Model) contentFor(s *registry.Session) string {
	if content, live := m.tabbed.Preview.Capture(); live &&
		s.TmuxName == m.tabbed.Preview.WatchedName() {
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

// churnStat is a worktree session's added/removed line totals against its base
// commit, the +N/−M the list row shows. It is recomputed on the churn tick and
// never persisted: it is a live view of the worktree, derived fresh each time.
type churnStat struct {
	added   int
	removed int
}

// churnTarget is the minimal set of facts the churn command needs to numstat one
// worktree session, gathered on the main goroutine (which reads the registry)
// before the command fans out, so the workers touch only their own copies.
type churnTarget struct {
	sessionID    string
	repoPath     string
	worktreePath string
	baseCommit   string
	workspaceID  string
}

// churnCmd computes the +N/−M churn of every worktree session concurrently and
// returns it as a churnMsg. It returns nil — issuing no git commands — when no
// worktree session is present, so a user living entirely in plain sessions pays
// nothing on the tick. Each session is numstatted in its own goroutine, joined
// before the message is returned, so a slow repository does not serialize the
// tick. A session whose numstat errors is omitted rather than failing the batch.
func (m Model) churnCmd() tea.Cmd {
	targets := m.churnTargets()
	if len(targets) == 0 {
		return nil
	}
	home := m.home
	return func() tea.Msg {
		stats := make(map[string]churnStat, len(targets))
		var (
			mu sync.Mutex
			wg sync.WaitGroup
		)
		for _, t := range targets {
			wg.Add(1)
			go func(t churnTarget) {
				defer wg.Done()
				added, removed, err := worktree.New(
					t.repoPath, home, t.workspaceID,
				).DiffNumstat(t.worktreePath, t.baseCommit)
				if err != nil {
					return
				}
				mu.Lock()
				stats[t.sessionID] = churnStat{added: added, removed: removed}
				mu.Unlock()
			}(t)
		}
		wg.Wait()
		return churnMsg{stats: stats}
	}
}

// churnTargets gathers the worktree sessions whose churn the tick recomputes: a
// session is included only when it has the branch, worktree and base commit a
// diff needs and its workspace (the repository the diff runs against) resolves.
// Plain sessions and sessions whose workspace has gone away are skipped, so the
// returned slice being empty is exactly the "no git on tick" case.
func (m Model) churnTargets() []churnTarget {
	var targets []churnTarget
	for _, s := range m.reg.ListSessions() {
		if s.Branch == "" || s.WorktreePath == "" || s.BaseCommit == "" {
			continue
		}
		ws, ok := m.reg.Workspace(s.WorkspaceID)
		if !ok {
			continue
		}
		targets = append(targets, churnTarget{
			sessionID:    s.ID,
			repoPath:     ws.RepoPath,
			worktreePath: s.WorktreePath,
			baseCommit:   s.BaseCommit,
			workspaceID:  s.WorkspaceID,
		})
	}
	return targets
}

// applyChurn replaces the cached churn stats with a freshly computed batch,
// dropping any entry whose session no longer exists so a delivery that races a
// session deletion cannot revive a stale row.
func (m *Model) applyChurn(msg churnMsg) {
	existing := make(map[string]bool)
	for _, s := range m.reg.ListSessions() {
		existing[s.ID] = true
	}
	next := make(map[string]churnStat, len(msg.stats))
	for id, c := range msg.stats {
		if existing[id] {
			next[id] = c
		}
	}
	m.churn = next
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
