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
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/joakimcarlsson/wasa/internal/backend"
	"github.com/joakimcarlsson/wasa/internal/launch"
	"github.com/joakimcarlsson/wasa/internal/registry"
)

// mode is the model's interaction mode: browsing the session list or filling in
// the create form.
type mode int

const (
	modeList mode = iota
	modeCreate
	modeConfirm
)

// Model is the cockpit's Bubble Tea model. It holds the registry it drives, the
// most-recently-used workspaces snapshot, the active workspace (tracked by id so
// it follows the workspace when an attach or create reorders the tabs), and the
// list cursor.
//
// The live-preview fields track the selected session's output stream: watchName
// is the session the preview targets (its tmux name, or "" for none); watcher is
// the live control-mode stream for it, or nil when streaming is unavailable,
// failed or dropped, in which case the fallback tick polls Capture; watchGen
// tags the active stream so a previewMsg from a superseded stream is ignored.
type Model struct {
	home   string
	reg    *registry.Registry
	tmux   backend.SessionBackend
	stream backend.StreamingBackend

	workspaces []*registry.Workspace
	activeID   string
	cursor     int

	mode    mode
	form    createForm
	confirm confirmDialog

	confirmCmd tea.Cmd

	width  int
	height int

	preview string

	watcher   backend.Watcher
	watchName string
	watchGen  int

	status string
	err    error
}

// New builds a cockpit model over reg. currentID is the workspace for the
// repository wasa was launched in; it becomes the initially active tab when
// present, otherwise the most-recently-used workspace is.
func New(home string, reg *registry.Registry, currentID string) Model {
	be := backend.Default()
	m := Model{home: home, reg: reg, tmux: be}
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
func Run(home string, reg *registry.Registry, currentID string) error {
	_, err := tea.NewProgram(New(home, reg, currentID), tea.WithAltScreen()).
		Run()
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
		return m, nil

	case tickMsg:
		return m, tea.Batch(tick(), m.pollOrReconnect())

	case previewMsg:
		return m, m.applyPreview(msg)

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
	}

	switch m.mode {
	case modeCreate:
		return m.updateCreate(msg)
	case modeConfirm:
		return m.updateConfirm(msg)
	}
	return m.updateList(msg)
}

func (m Model) updateList(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch key.String() {
	case "q", "ctrl+c":
		m.closeWatcher()
		return m, tea.Quit
	case "right", "tab", "]":
		m.cycleTab(1)
	case "left", "shift+tab", "[":
		m.cycleTab(-1)
	case "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down":
		if m.cursor < len(m.sessions())-1 {
			m.cursor++
		}
	case "n":
		return m.enterCreate()
	case "enter":
		return m.attach()
	case "k":
		return m.enterConfirmKill()
	case "d":
		return m.enterConfirmDelete()
	}
	return m, m.ensureWatcher()
}

func (m Model) updateCreate(msg tea.Msg) (tea.Model, tea.Cmd) {
	form, result, cmd := m.form.update(msg)
	m.form = form
	switch result {
	case formCancel:
		m.mode = modeList
		return m, nil
	case formSubmit:
		ws := m.currentWorkspace()
		params := m.form.params()
		m.mode = modeList
		m.status = "creating session…"
		return m, m.createCmd(ws, params)
	}
	return m, cmd
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
func (m Model) enterConfirm(dialog confirmDialog, onConfirm tea.Cmd) (tea.Model, tea.Cmd) {
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

// enterCreate opens the create form. With a current workspace the form is seeded
// with that workspace's profiles and its repository as the default working
// directory; with no workspace — wasa launched outside any git repository — the
// form opens with no profiles and the current directory as the default, so a
// plain session can be created anywhere. ws being nil is the no-repo path, not
// an error.
func (m Model) enterCreate() (tea.Model, tea.Cmd) {
	ws := m.currentWorkspace()

	var (
		names      []string
		defaultDir string
	)
	if ws != nil {
		names = make([]string, len(ws.Profiles))
		for i, p := range ws.Profiles {
			names[i] = p.Name
		}
		defaultDir = ws.RepoPath
	} else if cwd, err := os.Getwd(); err == nil {
		defaultDir = cwd
	}

	m.form = newCreateForm(names, defaultDir)
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
	return func() tea.Msg {
		if err := launch.KillSession(reg, s); err != nil {
			return killedMsg{err: err}
		}
		if err := reg.Save(); err != nil {
			return killedMsg{err: err}
		}
		return killedMsg{}
	}
}

func (m Model) deleteCmd(s *registry.Session) tea.Cmd {
	reg := m.reg
	return func() tea.Msg {
		if err := launch.DeleteSession(reg, s); err != nil {
			return deletedMsg{err: err}
		}
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
// session's, or "" when nothing running is selected.
func (m Model) previewTarget() string {
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

// pollCapture re-captures the selected session with a one-shot Capture. A
// non-running or absent session clears the buffer. Errors are swallowed: the
// preview is a convenience, not a source of truth. This is the fallback when no
// stream is available; on the streaming path it does not run.
func (m *Model) pollCapture() {
	s := m.selectedSession()
	if s == nil || s.Status != registry.StatusRunning {
		m.preview = ""
		return
	}
	if out, err := m.tmux.Capture(s.TmuxName); err == nil {
		m.preview = out
	}
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
