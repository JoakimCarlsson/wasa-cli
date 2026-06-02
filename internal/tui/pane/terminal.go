package pane

import (
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/joakimcarlsson/wasa/internal/backend"
	"github.com/joakimcarlsson/wasa/internal/launch"
	"github.com/joakimcarlsson/wasa/internal/tui/theme"
)

// TermMsg carries the result of ensuring and capturing a session's companion
// shell. name is the companion's tmux name (so a delivery for a session no
// longer selected is ignored), content is its pane capture, and err is set when
// the companion could not be spawned or addressed.
type TermMsg struct {
	name    string
	content string
	err     error
}

// TermSession is the minimal set of facts the Terminal body needs about the
// selected session to choose its render state without the pane reaching into
// the registry: whether one is selected and its companion tmux name, so a
// capture from a previously selected session is not shown as if it were this
// one's.
type TermSession struct {
	Selected      bool
	CompanionName string
}

// Terminal owns the companion-shell state for the Terminal tab: the companion
// whose capture is currently shown, that capture's content, and the set of
// companions this run has spawned so they can be torn down on quit.
type Terminal struct {
	shown   string
	content string
	terms   map[string]bool
}

// NewTerminal builds a Terminal with an empty companion set.
func NewTerminal() Terminal {
	return Terminal{terms: make(map[string]bool)}
}

// EnsureCmd returns a command that lazily spawns the selected session's
// companion shell — a tmux session distinct from the agent's, named off
// sessionTmux, running launch.Shell() in dir (the root passes the session's
// worktree or working directory) — when one does not already exist, then
// captures it for the Terminal tab body. An existing companion is reused rather
// than respawned, so it survives cockpit restarts. An empty sessionTmux (no
// session selected) clears the body.
func (t *Terminal) EnsureCmd(
	sessionTmux, dir string,
	be backend.SessionBackend,
) tea.Cmd {
	if sessionTmux == "" {
		return func() tea.Msg { return TermMsg{} }
	}
	name := companionName(sessionTmux)
	return func() tea.Msg {
		has, err := be.Has(name)
		if err != nil {
			return TermMsg{name: name, err: err}
		}
		if !has {
			if err := be.SpawnEnv(name, dir, nil, launch.Shell()); err != nil {
				return TermMsg{name: name, err: err}
			}
		}
		out, _ := be.Capture(name)
		return TermMsg{name: name, content: out}
	}
}

// Apply stores a companion capture for rendering and records the companion as
// live so it is torn down on exit. A delivery whose companion is not expected
// (no longer the selected session's) is dropped, so a late capture cannot
// overwrite the body after the selection moved; the root passes the companion
// name of the current selection as expected. A spawn or address error is
// returned for the root to surface on the status line.
func (t *Terminal) Apply(msg TermMsg, expected string) (tea.Cmd, error) {
	if msg.err != nil {
		return nil, msg.err
	}
	if msg.name == "" {
		t.shown = ""
		t.content = ""
		return nil, nil
	}
	t.terms[msg.name] = true
	if msg.name != expected {
		return nil, nil
	}
	t.shown = msg.name
	t.content = msg.content
	return nil, nil
}

// AttachCmd ensures the selected session's companion shell exists — spawning it
// in dir when missing — then returns the unstarted command that attaches to it
// and records it for teardown. The root wraps the command in tea.ExecProcess so
// Bubble Tea releases the terminal for the attach and resumes on detach. The
// companion is independent of the agent, so it attaches even when the agent
// session itself has exited.
func (t *Terminal) AttachCmd(
	sessionTmux, dir string, be backend.SessionBackend,
) (*exec.Cmd, error) {
	name := companionName(sessionTmux)
	switch has, err := be.Has(name); {
	case err != nil:
		return nil, err
	case !has:
		if err := be.SpawnEnv(name, dir, nil, launch.Shell()); err != nil {
			return nil, err
		}
	}
	t.terms[name] = true
	return be.AttachCmd(name)
}

// Close kills every companion shell this run spawned. The root calls it on quit
// so no wasa_*_term sessions are left behind. Each kill is best-effort: a
// companion a session kill or delete already removed is gone, and the backend's
// error for a missing session is swallowed.
func (t *Terminal) Close(be backend.SessionBackend) {
	for name := range t.terms {
		_ = be.Kill(name)
	}
	t.terms = make(map[string]bool)
}

// Tracking reports whether the companion named name is recorded for teardown.
// It exists for tests; the root does not read it.
func (t Terminal) Tracking(name string) bool {
	return t.terms[name]
}

// Body renders the Terminal tab: a capture of the selected session's companion
// shell. With no session selected it says so. Until the first capture for the
// current selection arrives it shows a starting hint, so a stale capture from a
// previously selected session is never shown as if it were this one's.
func (t Terminal) Body(theme theme.Theme, sess TermSession, w, h int) string {
	if !sess.Selected {
		return theme.DimStyle.Render("No session selected.")
	}
	if t.shown != sess.CompanionName ||
		strings.TrimSpace(ansi.Strip(t.content)) == "" {
		return theme.DimStyle.Render("Starting shell…")
	}
	return renderCapture(t.content, w, h)
}

// companionName is the deterministic tmux name of a session's companion shell:
// its agent tmux name with a _term suffix. Deriving it from the stable tmux
// name keeps it identical across cockpit restarts and distinct from the agent
// session, so the two never collide.
func companionName(sessionTmux string) string {
	return sessionTmux + "_term"
}
