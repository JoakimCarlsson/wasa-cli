package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/joakimcarlsson/wasa/internal/backend"
	"github.com/joakimcarlsson/wasa/internal/launch"
	"github.com/joakimcarlsson/wasa/internal/registry"
)

// termMsg carries the result of ensuring and capturing a session's companion
// shell. name is the companion's tmux name (so a delivery for a session no
// longer selected is ignored), content is its pane capture, and err is set when
// the companion could not be spawned or addressed.
type termMsg struct {
	name    string
	content string
	err     error
}

// terminalPane is the companion-shell feature machine: it owns the captured
// body of the selected session's companion terminal and the set of companions
// this run spawned, so they can be torn down on quit. shown is the companion the
// body currently belongs to, so a stale capture from a previously selected
// session is never shown as if it were this one's.
type terminalPane struct {
	tmux backend.SessionBackend

	shown   string
	content string
	spawned map[string]bool
}

func newTerminalPane(tmux backend.SessionBackend) terminalPane {
	return terminalPane{tmux: tmux, spawned: make(map[string]bool)}
}

// companionName is the deterministic tmux name of a session's companion shell:
// its agent TmuxName with a _term suffix. Deriving it from the stable TmuxName
// keeps it identical across cockpit restarts and distinct from the agent
// session, so the two never collide.
func companionName(s *registry.Session) string {
	return s.TmuxName + "_term"
}

// ensure returns a command that lazily spawns s's companion shell — a tmux
// session distinct from the agent's, named off its TmuxName, running
// launch.Shell() in the session's worktree (or working) directory — when one
// does not already exist, then captures it for the Terminal tab body. An
// existing companion is reused rather than respawned, so it survives cockpit
// restarts. With no session selected it clears the body.
func (t *terminalPane) ensure(s *registry.Session) tea.Cmd {
	if s == nil {
		return func() tea.Msg { return termMsg{} }
	}
	name := companionName(s)
	dir := s.WorktreePath
	if dir == "" {
		dir = s.WorkingDir
	}
	be := t.tmux
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

// apply stores a companion capture for rendering and records the companion as
// live so it is torn down on exit. A delivery whose companion is no longer the
// selected session's is dropped, so a late capture cannot overwrite the body
// after the selection moved. The error, if any, is returned to the caller to
// surface on the status line.
func (t *terminalPane) apply(msg termMsg, s *registry.Session) error {
	if msg.err != nil {
		return msg.err
	}
	if msg.name == "" {
		t.shown = ""
		t.content = ""
		return nil
	}
	t.spawned[msg.name] = true
	if s == nil || companionName(s) != msg.name {
		return nil
	}
	t.shown = msg.name
	t.content = msg.content
	return nil
}

// prepareAttach ensures s's companion shell exists, spawning it first if needed,
// and returns its tmux name so the caller can hand the terminal to it through
// tea.ExecProcess. The companion is recorded as live so it is cleaned up on
// quit.
func (t *terminalPane) prepareAttach(s *registry.Session) (string, error) {
	name := companionName(s)
	dir := s.WorktreePath
	if dir == "" {
		dir = s.WorkingDir
	}
	switch has, err := t.tmux.Has(name); {
	case err != nil:
		return "", err
	case !has:
		if err := t.tmux.SpawnEnv(name, dir, nil, launch.Shell()); err != nil {
			return "", err
		}
	}
	t.spawned[name] = true
	return name, nil
}

// close kills every companion shell this run spawned. It runs on quit so no
// wasa_*_term sessions are left behind. Each kill is best-effort: a companion a
// session kill or delete already removed is gone, and tmux's error for a missing
// session is swallowed.
func (t *terminalPane) close() {
	for name := range t.spawned {
		_ = t.tmux.Kill(name)
	}
	t.spawned = make(map[string]bool)
}

// view renders the Terminal tab: a capture of the selected session's companion
// shell. Until the first capture for the current selection arrives it shows a
// starting hint.
func (t terminalPane) view(th Theme, s *registry.Session, w, h int) string {
	if s == nil {
		return th.dimStyle.Render("No session selected.")
	}
	if t.shown != companionName(s) ||
		strings.TrimSpace(ansi.Strip(t.content)) == "" {
		return th.dimStyle.Render("Starting shell…")
	}
	return renderCapture(t.content, w, h)
}
