package tui

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/joakimcarlsson/wasa-cli/internal/backend"
	"github.com/joakimcarlsson/wasa-cli/internal/collision"
	"github.com/joakimcarlsson/wasa-cli/internal/config"
	"github.com/joakimcarlsson/wasa-cli/internal/launch"
	"github.com/joakimcarlsson/wasa-cli/internal/record"
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
	modeCheckpoints
	modeCheckpointSearch
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

	// checkpoints backs the checkpoints browser (modeCheckpoints): the record of
	// the active workspace's repo, browsable read-only without leaving the
	// cockpit. It is built fresh on open and discarded on close.
	checkpoints checkpointsState

	// checkpointSearch backs the checkpoint search overlay (modeCheckpointSearch):
	// a debounced fuzzy search over the active workspace's record whose result
	// opens the browser on that checkpoint. Built fresh on open, cleared on close.
	checkpointSearch checkpointSearchState

	confirmCmd     tea.Cmd
	confirmPending string

	width  int
	height int

	tabbed pane.Tabbed

	now          func() time.Time
	statuses     *sessionstatus.Tracker
	notify       func(title, body string)
	lastNotifyAt map[string]time.Time
	lastStatus   map[string]sessionstatus.Status
	churn        map[string]churnStat

	// collisions maps a session ID to the other live worktree sessions in its
	// workspace that share at least one changed path with it, recomputed on
	// the churn tick from the same Numstat read that fills churn — see
	// internal/collision. A session absent from the map has no collision.
	collisions map[string][]collision.Overlap

	// recording maps a workspace ID to the recording agents wired in its repo
	// (from record.InstalledAgents). It is derived, not stored: rebuilt on
	// refresh and every tick so a change made by `wasa record` in another
	// terminal converges without live filesystem watching. An empty or absent
	// entry means recording is off for that workspace.
	recording map[string][]string

	// recorded maps a session ID to its newest checkpoint entry, across all
	// workspace repos. Like recording it is derived, not stored: rebuilt from
	// record.List on refresh and every tick so a checkpoint written by a
	// finishing session shows up here within a tick without live watching. An
	// absent entry means the session produced no checkpoint (recording off, or
	// none written) — absence is the signal, never an error state.
	recorded map[string]record.Entry

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
		collisions:   make(map[string][]collision.Overlap),
		recording:    make(map[string][]string),
		recorded:     make(map[string]record.Entry),
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
	m.refreshRecording()
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
	_, err := tea.NewProgram(New(home, reg, currentID, cfg)).Run()
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
	stats   map[string]churnStat
	changed map[string][]string
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
	// collision is set when err is a worktree-already-exists collision,
	// carrying enough to offer clearing the stale worktree and retrying.
	collision  launch.WorktreeCollision
	collidesWs *registry.Workspace
	retry      launch.Params
}

type killedMsg struct{ err error }

type deletedMsg struct{ err error }

// pausedMsg carries the outcome of a pause. retry, when set, is the session
// whose non-forced pause was blocked (typically by a dirty worktree); the
// update loop offers a force retry for it.
type pausedMsg struct {
	err   error
	retry *registry.Session
}

type resumedMsg struct{ err error }

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

// recordToggledMsg carries the outcome of toggling repo-level recording for a
// workspace back to the update loop. enabled says which direction the toggle
// went; agents is the wired tool set when it was turned on; none is set when the
// toggle was a no-op because no supported agent is on PATH (mirroring the
// `wasa record enable` behaviour, but as a transient message rather than an
// error).
type recordToggledMsg struct {
	wsName  string
	enabled bool
	agents  []string
	none    bool
	err     error
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.sizeDiffViewport()
		m.sizeCheckpoints()
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
			if msg.collision.Path != "" {
				return m.enterConfirmClearWorktree(msg)
			}
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

	case pausedMsg:
		if msg.err != nil {
			if msg.retry != nil {
				return m.enterConfirmForcePause(msg.retry, msg.err)
			}
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.status = "paused session — branch kept, worktree freed"
		return m, m.refresh()

	case resumedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.status = "resumed session"
		return m, m.refresh()

	case workspaceDeletedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.status = "deleted workspace " + msg.name
		return m, m.refresh()

	case recordToggledMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		switch {
		case msg.none:
			m.status = "no supported agents found on PATH"
		case msg.enabled:
			m.status = "recording enabled for " + msg.wsName +
				" (" + strings.Join(msg.agents, ", ") + ")"
		default:
			m.status = "recording disabled for " + msg.wsName
		}
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

	case ckptSearchTickMsg:
		if m.mode == modeCheckpointSearch {
			return m.checkpointSearchTick(msg.gen)
		}
		return m, nil

	case ckptSearchResultMsg:
		if m.mode == modeCheckpointSearch {
			return m.applyCheckpointSearchResult(msg)
		}
		return m, nil

	case modal.ConfirmAcceptedMsg:
		cmd := m.confirmCmd
		m.mode = modeList
		m.confirmCmd = nil
		if m.confirmPending != "" {
			m.status = m.confirmPending
			m.confirmPending = ""
		}
		return m, cmd

	case modal.ConfirmCancelledMsg:
		m.mode = modeList
		m.confirmCmd = nil
		m.confirmPending = ""
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
	case modeCheckpoints:
		return m.updateCheckpoints(msg)
	case modeCheckpointSearch:
		return m.updateCheckpointSearch(msg)
	}
	return m.updateList(msg)
}

func (m Model) updateList(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.filter.active {
		return m.updateFilter(msg)
	}

	key, ok := msg.(tea.KeyPressMsg)
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
	case config.ActionRecordToggle:
		return m.toggleRecording()
	case config.ActionCheckpoints:
		return m.enterCheckpoints()
	case config.ActionCheckpointSearch:
		return m.enterCheckpointSearch()
	case config.ActionNew:
		return m.enterCreate()
	case config.ActionAttach:
		return m.attach()
	case config.ActionKill:
		return m.enterConfirmKill()
	case config.ActionDelete:
		return m.enterConfirmDelete()
	case config.ActionPause:
		return m.enterConfirmPause()
	case config.ActionResume:
		return m.resume()
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
	if m.cfg.History.Enabled {
		params.HistoryMaxBytes = m.cfg.History.MaxBytes
	}
	if m.cfg.Collision.Enabled {
		params.CollisionMaxPaths = m.cfg.Collision.MaxPaths
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
		"initializing repository…",
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
		fmt.Sprintf("deleting workspace %q…", ws.Name),
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

// toggleRecording flips repo-level recording for the active workspace, off the
// update loop. It is a no-op on the orphan tab (no workspace). The command reads
// the current state fresh (record.InstalledAgents) rather than trusting the
// cached m.recording map, so a concurrent CLI change can't make the toggle act
// on stale state, and routes both directions through the same record package the
// `wasa record` command uses.
func (m Model) toggleRecording() (tea.Model, tea.Cmd) {
	ws := m.currentWorkspace()
	if ws == nil {
		return m, nil
	}
	dir, name := ws.RepoPath, ws.Name
	return m, func() tea.Msg {
		if len(record.InstalledAgents(dir)) > 0 {
			if err := record.RemoveHooks(dir); err != nil {
				return recordToggledMsg{wsName: name, err: err}
			}
			return recordToggledMsg{wsName: name, enabled: false}
		}
		agents, err := record.Enable(dir)
		if err != nil {
			return recordToggledMsg{wsName: name, err: err}
		}
		if len(agents) == 0 {
			return recordToggledMsg{wsName: name, none: true}
		}
		return recordToggledMsg{wsName: name, enabled: true, agents: agents}
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
		"deleting session…",
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
		"killing session…",
	)
}

// enterConfirmPause opens the pause-confirmation modal for the selected
// session. Any non-paused session can be paused: a running one is soft-stopped,
// and an exited one that still owns a worktree has it freed — which is also the
// recovery path for a session stranded by a pause that failed after its tmux
// died. The first attempt never discards uncommitted changes: a dirty worktree
// blocks it and routes to the force confirmation instead.
func (m Model) enterConfirmPause() (tea.Model, tea.Cmd) {
	s := m.selectedSession()
	if s == nil || s.Status == registry.StatusPaused {
		return m, nil
	}
	title, _ := sessionLabel(s)
	prompt := fmt.Sprintf(
		"Pause %q?\nIts tmux stops and the worktree is removed. The branch "+
			"and its commits are kept; resume rebuilds it.",
		title,
	)
	if s.WorktreePath == "" {
		prompt = fmt.Sprintf(
			"Pause %q?\nIts tmux stops; resume restarts it.", title,
		)
	}
	return m.enterConfirm(
		modal.NewConfirmDialog(
			m.theme, "Pause session", confirmBody(m.theme, prompt, s),
			"Pause", "Cancel", true,
		),
		m.pauseCmd(s, false),
		"pausing session…",
	)
}

// enterConfirmForcePause opens the second, force-pause confirmation after a
// non-forced pause was blocked — typically by uncommitted worktree changes.
// Only this explicit second confirmation discards them, mirroring how finish
// treats a dirty worktree as an error unless the caller opts into force.
// Cancelling leaves the session's worktree intact; its tmux was already
// stopped by the failed attempt, so pressing pause again retries from there.
func (m Model) enterConfirmForcePause(
	s *registry.Session, cause error,
) (tea.Model, tea.Cmd) {
	title, _ := sessionLabel(s)
	body := confirmBody(m.theme, fmt.Sprintf(
		"Pause of %q was blocked:\n%s\n\nDiscard the worktree's uncommitted "+
			"changes and pause anyway?",
		title, cause,
	), s)
	return m.enterConfirm(
		modal.NewConfirmDialog(
			m.theme, "Force pause", body, "Discard and pause", "Cancel", true,
		),
		m.pauseCmd(s, true),
		"pausing session…",
	)
}

// enterConfirmClearWorktree opens the confirm dialog offering to clear the
// worktree that collided with a session create, then retry it, after
// createCmd's Add reported *worktree.ErrWorktreeExists. It names the existing
// session when one still owns the worktree — the common case, a session from an
// earlier run of the same branch left behind by a crash or a skipped teardown —
// and otherwise just names the stale path. A collision whose session is still
// tmux-alive is called out explicitly in the body so accepting is an informed
// choice, not a surprise kill of a running session; cancelling always leaves
// the existing worktree and session untouched.
func (m Model) enterConfirmClearWorktree(msg createdMsg) (tea.Model, tea.Cmd) {
	col := msg.collision
	what := fmt.Sprintf("the worktree at %s", col.Path)
	if col.Session != nil {
		title, _ := sessionLabel(col.Session)
		what = fmt.Sprintf("session %q (worktree at %s)", title, col.Path)
		if col.Alive {
			what += " — currently running"
		}
	}
	body := fmt.Sprintf(
		"A worktree for branch %q already exists:\n%s.\n\n"+
			"Clear it and create the new session, or cancel and leave it "+
			"in place?",
		col.Branch, what,
	)
	return m.enterConfirm(
		modal.NewConfirmDialog(
			m.theme, "Worktree already exists", body,
			"Clear and create", "Cancel", true,
		),
		m.clearWorktreeCollisionCmd(msg),
		"clearing worktree…",
	)
}

// resume re-spawns the selected paused session. It is deliberately confirm-less:
// resuming is additive and destroys nothing. On a session that is not paused it
// is a no-op with a clear message rather than an error.
func (m Model) resume() (tea.Model, tea.Cmd) {
	s := m.selectedSession()
	if s == nil {
		return m, nil
	}
	if s.Status == registry.StatusRunning {
		m.status = "session is already running"
		return m, nil
	}
	if s.Status != registry.StatusPaused {
		m.status = "only a paused session can be resumed"
		return m, nil
	}
	m.err = nil
	m.status = "resuming session…"
	return m, m.resumeCmd(s)
}

// enterConfirm opens dialog as a modal and stores onConfirm as the command to
// run if it is accepted. pending is the in-progress status shown from the
// moment the dialog is accepted until onConfirm's result message lands; an
// empty pending leaves the status untouched, as before.
func (m Model) enterConfirm(
	dialog modal.ConfirmDialog,
	onConfirm tea.Cmd,
	pending string,
) (tea.Model, tea.Cmd) {
	m.confirm = dialog
	m.confirmCmd = onConfirm
	m.confirmPending = pending
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
			if col, ok := launch.DetectWorktreeCollision(reg, ws, err); ok {
				return createdMsg{
					err: err, collision: col, collidesWs: ws, retry: params,
				}
			}
			return createdMsg{err: err}
		}
		if err := reg.Save(); err != nil {
			return createdMsg{err: err}
		}
		return createdMsg{session: s}
	}
}

// clearWorktreeCollisionCmd tears down whatever occupies msg.collision's path
// — a stale worktree, or a recorded session's tmux, worktree and record
// together — and then retries the create with the same params, so accepting
// the confirm dialog both fixes the collision and finishes the original
// request in one step.
func (m Model) clearWorktreeCollisionCmd(msg createdMsg) tea.Cmd {
	home, reg, ws := m.home, m.reg, msg.collidesWs
	col, params := msg.collision, msg.retry
	return func() tea.Msg {
		if err := launch.ClearWorktreeCollision(
			reg,
			home,
			ws,
			col,
		); err != nil {
			return createdMsg{err: err}
		}
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

// pauseCmd soft-stops the session: companion shell and tmux killed, worktree
// removed, branch and registry record kept, status set to paused. The companion
// dies before the worktree is removed so no process holds the tree while git
// tears it down; if the pause then fails, the Terminal tab respawns the
// companion on demand. When force is false a blocked worktree removal does not
// discard anything: the failure routes back as a retry so only the explicit
// force confirmation discards uncommitted changes.
func (m Model) pauseCmd(s *registry.Session, force bool) tea.Cmd {
	reg, home := m.reg, m.home
	be := m.tmux
	term := companionName(s.TmuxName)
	return func() tea.Msg {
		_ = be.Kill(term)
		if err := launch.PauseSession(reg, be, home, s, force); err != nil {
			if !force {
				return pausedMsg{err: err, retry: s}
			}
			return pausedMsg{err: err}
		}
		if err := reg.Save(); err != nil {
			return pausedMsg{err: err}
		}
		return pausedMsg{}
	}
}

// resumeCmd rebuilds the paused session — worktree, bootstrap, env, hook — and
// re-spawns its tmux, persisting the registry once it is running again.
func (m Model) resumeCmd(s *registry.Session) tea.Cmd {
	reg, home := m.reg, m.home
	return func() tea.Msg {
		if err := launch.ResumeSession(home, reg, s); err != nil {
			return resumedMsg{err: err}
		}
		if err := reg.Save(); err != nil {
			return resumedMsg{err: err}
		}
		return resumedMsg{}
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
	m.refreshRecording()
	return m.tabbed.Preview.SetTarget(m.previewTarget())
}

// refreshRecording rebuilds the derived per-workspace recording state from the
// filesystem (record.InstalledAgents) and the per-session checkpoint index from
// the ref store (record.List). Both are cheap — a handful of stats plus one ref
// walk per workspace — and run on refresh and every tick, so recording toggled
// from a `wasa record` command in another terminal, or a checkpoint written by a
// finishing session, shows up here within a tick without live watching.
//
// record.List returns the newest checkpoint per session; a repo with no record
// yields an empty list. Errors are treated as "no record" — the indicator's
// absence is the correct fallback, never an error state.
func (m *Model) refreshRecording() {
	next := make(map[string][]string, len(m.workspaces))
	recorded := make(map[string]record.Entry)
	for _, w := range m.workspaces {
		if agents := record.InstalledAgents(w.RepoPath); len(agents) > 0 {
			next[w.ID] = agents
		}
		entries, _ := record.List(w.RepoPath)
		for _, e := range entries {
			if e.Meta.SessionID != "" {
				recorded[e.Meta.SessionID] = e
			}
		}
	}
	m.recording = next
	m.recorded = recorded
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
	if m.reg.Reconcile(backend.ExitProbe(m.tmux)) {
		_ = m.reg.Save()
	}
	m.refreshRecording()

	focused := m.focusedSessionID()
	keep := make(map[string]bool)
	for _, s := range m.reg.ListSessions() {
		keep[s.ID] = true
		if s.Status == registry.StatusPaused {
			m.transition(s, sessionstatus.Paused, focused)
			continue
		}
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

// churnCmd computes the +N/−M churn and the changed-path set of every worktree
// session concurrently and returns it as a churnMsg, reading git once per
// session (Numstat) for both. It returns nil — issuing no git commands — when
// no worktree session is present, so a user living entirely in plain sessions
// pays nothing on the tick. Each session is numstatted in its own goroutine,
// joined before the message is returned, so a slow repository does not
// serialize the tick. A session whose numstat errors is omitted from both maps
// rather than failing the batch — the same contract collision detection needs.
func (m Model) churnCmd() tea.Cmd {
	targets := m.churnTargets()
	if len(targets) == 0 {
		return nil
	}
	home := m.home
	return func() tea.Msg {
		stats := make(map[string]churnStat, len(targets))
		changed := make(map[string][]string, len(targets))
		var (
			mu sync.Mutex
			wg sync.WaitGroup
		)
		for _, t := range targets {
			wg.Add(1)
			go func(t churnTarget) {
				defer wg.Done()
				entries, err := worktree.New(
					t.repoPath, home, t.workspaceID,
				).Numstat(t.worktreePath, t.baseCommit)
				if err != nil {
					return
				}
				var added, removed int
				paths := make([]string, 0, len(entries))
				for _, e := range entries {
					added += e.Added
					removed += e.Removed
					paths = append(paths, e.Path)
				}
				mu.Lock()
				stats[t.sessionID] = churnStat{added: added, removed: removed}
				changed[t.sessionID] = paths
				mu.Unlock()
			}(t)
		}
		wg.Wait()
		return churnMsg{stats: stats, changed: changed}
	}
}

// churnTargets gathers the worktree sessions whose churn the tick recomputes: a
// session is included only when it has the branch, worktree and base commit a
// diff needs and its workspace (the repository the diff runs against) resolves.
// Plain sessions, paused sessions (their worktree is removed, so a numstat
// could only error) and sessions whose workspace has gone away are skipped, so
// the returned slice being empty is exactly the "no git on tick" case.
func (m Model) churnTargets() []churnTarget {
	var targets []churnTarget
	for _, s := range m.reg.ListSessions() {
		if s.Status == registry.StatusPaused {
			continue
		}
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
// session deletion cannot revive a stale row. A paused session is absent from
// the batch (its worktree is gone, so the tick skips it); its last-known churn
// is carried over so the row freezes at the pre-pause numbers rather than
// blanking.
func (m *Model) applyChurn(msg churnMsg) {
	next := make(map[string]churnStat, len(msg.stats))
	sessions := m.reg.ListSessions()
	for _, s := range sessions {
		if c, ok := msg.stats[s.ID]; ok {
			next[s.ID] = c
			continue
		}
		if c, ok := m.churn[s.ID]; ok && s.Status == registry.StatusPaused {
			next[s.ID] = c
		}
	}
	m.churn = next
	m.collisions = collision.Compute(sessions, msg.changed)
}

// runtimeStatus is the status the list renders for a session: paused or exited
// when the registry says so (the persisted source of truth), otherwise the
// derived runtime status, falling back to working for a running session not yet
// observed.
func (m Model) runtimeStatus(s *registry.Session) sessionstatus.Status {
	if s.Status == registry.StatusPaused {
		return sessionstatus.Paused
	}
	if s.Status != registry.StatusRunning {
		return sessionstatus.Exited
	}
	if st, ok := m.lastStatus[s.ID]; ok {
		return st
	}
	return sessionstatus.Working
}
