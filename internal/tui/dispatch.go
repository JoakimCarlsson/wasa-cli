package tui

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/joakimcarlsson/wasa-cli/internal/launch"
	"github.com/joakimcarlsson/wasa-cli/internal/registry"
)

// DispatchMsg asks the cockpit to create a session for a control-plane
// dispatch. It travels through the Bubble Tea program so the create runs on
// the same path — and the same goroutines — as a TUI-initiated create; the
// registry is not safe for concurrent use, so the link layer never touches
// it directly. Reply always receives exactly one result.
type DispatchMsg struct {
	WorkspaceID string
	Intent      string
	Program     string
	Reply       chan<- DispatchResult
}

// DispatchResult is the cockpit's answer to a DispatchMsg. An empty Code
// means success; otherwise Code matches the wire protocol's dispatch error
// codes and Message is for humans.
type DispatchResult struct {
	SessionID string
	Code      string
	Message   string
}

// SessionTargetMsg resolves a session id to its backend (tmux) target name,
// again via the program so the registry read happens on the update goroutine.
// Reply always receives exactly one result; OK is false for an unknown or
// exited session.
type SessionTargetMsg struct {
	SessionID string
	Reply     chan<- SessionTarget
}

// SessionTarget is the answer to a SessionTargetMsg.
type SessionTarget struct {
	TmuxName string
	OK       bool
}

// Dispatch error codes, mirroring pkg/protocol's DispatchErr* values without
// importing it — the TUI stays wire-agnostic.
const (
	dispatchErrUnknownWorkspace = "unknown_workspace"
	dispatchErrLaunchFailed     = "launch_failed"
)

// handleDispatch validates the dispatch against the registry and launches
// the session through the same createCmd internals the create form uses. The
// intent becomes the agent's launch prompt and the session title.
func (m Model) handleDispatch(msg DispatchMsg) (tea.Model, tea.Cmd) {
	ws, ok := m.reg.Workspace(msg.WorkspaceID)
	if !ok {
		msg.Reply <- DispatchResult{
			Code:    dispatchErrUnknownWorkspace,
			Message: "no workspace " + msg.WorkspaceID + " on this runner",
		}
		return m, nil
	}
	agents := launch.DetectAgents()
	if len(agents) == 0 {
		msg.Reply <- DispatchResult{
			Code:    dispatchErrLaunchFailed,
			Message: "no coding agent found on the runner's PATH",
		}
		return m, nil
	}
	program := agents[0]
	if msg.Program != "" {
		if !slices.Contains(agents, msg.Program) {
			msg.Reply <- DispatchResult{
				Code: dispatchErrLaunchFailed,
				Message: "agent " + msg.Program +
					" is not available on this runner",
			}
			return m, nil
		}
		program = msg.Program
	}
	params := launch.Params{
		Branch:  dispatchBranch(msg.Intent),
		Title:   truncateIntent(msg.Intent),
		Program: program,
		Prompt:  msg.Intent,
	}
	return m, m.dispatchCmd(ws, params, msg.Reply)
}

// dispatchCmd is createCmd with the outcome additionally delivered to the
// dispatch's reply channel. The returned createdMsg drives the normal UI
// refresh, so a dispatched session appears in the cockpit like any other.
func (m Model) dispatchCmd(
	ws *registry.Workspace,
	params launch.Params,
	reply chan<- DispatchResult,
) tea.Cmd {
	home, reg := m.home, m.reg
	return func() tea.Msg {
		s, err := launch.CreateSession(home, reg, ws, params)
		if err != nil {
			reply <- DispatchResult{
				Code:    dispatchErrLaunchFailed,
				Message: err.Error(),
			}
			return createdMsg{err: err}
		}
		saveErr := reg.Save()
		reply <- DispatchResult{SessionID: s.ID}
		if saveErr != nil {
			return createdMsg{err: saveErr}
		}
		return createdMsg{session: s}
	}
}

// resolveSessionTarget answers a SessionTargetMsg from the registry.
func (m Model) resolveSessionTarget(msg SessionTargetMsg) {
	s, ok := m.reg.Session(msg.SessionID)
	if !ok || s.Status != registry.StatusRunning {
		msg.Reply <- SessionTarget{}
		return
	}
	msg.Reply <- SessionTarget{TmuxName: s.TmuxName, OK: true}
}

// dispatchBranch derives a worktree branch name from the intent: a short
// slug for humans plus a random suffix for uniqueness.
func dispatchBranch(intent string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(intent) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
		if b.Len() >= 24 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "dispatch"
	}
	return "wasa/" + slug + "-" + registry.NewSessionID()[:6]
}

// truncateIntent shortens the intent to a list-friendly session title.
func truncateIntent(intent string) string {
	intent = strings.Join(strings.Fields(intent), " ")
	const maxTitle = 60
	runes := []rune(intent)
	if len(runes) <= maxTitle {
		return intent
	}
	return string(runes[:maxTitle-1]) + "…"
}
