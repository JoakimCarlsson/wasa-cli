package pane

import (
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/joakimcarlsson/wasa-cli/internal/backend"
	"github.com/joakimcarlsson/wasa-cli/internal/launch"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/theme"
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

// TermSession is the minimal set of facts the Terminal body needs about its
// current target to choose its render state without the pane reaching into the
// registry: the companion tmux name the body expects — a session's companion,
// or the workspace's own root shell when no session is selected — so a capture
// from a previous target is not shown as if it were this one's, and whether
// that target is the workspace root, which the body labels. An empty
// CompanionName means there is nothing to show a shell for.
type TermSession struct {
	CompanionName string
	Root          bool
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

// EnsureCmd returns a command that lazily spawns the companion shell for base —
// a tmux session distinct from any agent's, named off base, running
// launch.Shell() in dir (the root passes the selected session's worktree, or the
// workspace's repository path when no session is selected) — when one does not
// already exist, then captures it for the Terminal tab body. An existing
// companion is reused rather than respawned, so it survives cockpit restarts. An
// empty base (no session and no workspace) clears the body.
func (t *Terminal) EnsureCmd(
	base, dir string,
	be backend.SessionBackend,
) tea.Cmd {
	if base == "" {
		return func() tea.Msg { return TermMsg{} }
	}
	name := companionName(base)
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
// (no longer the current target's) is dropped, so a late capture cannot
// overwrite the body after the selection moved; the root passes the companion
// name of the current target as expected. A spawn or address error is
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

// AttachCmd ensures the companion shell for base exists — spawning it in dir
// when missing — then returns the unstarted command that attaches to it and
// records it for teardown. The root wraps the command in tea.ExecProcess so
// Bubble Tea releases the terminal for the attach and resumes on detach. The
// companion is independent of the agent, so it attaches even when the agent
// session itself has exited, and a workspace root shell attaches with no
// session at all.
func (t *Terminal) AttachCmd(
	base, dir string, be backend.SessionBackend,
) (*exec.Cmd, error) {
	name := companionName(base)
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

// Body renders the Terminal tab: a capture of the target's companion shell —
// the selected session's, or the workspace's own shell at the repository root
// when none is selected, which is labelled so the working directory is never in
// doubt. With no target at all it says so. Until the first capture for the
// current target arrives it shows a starting hint, so a stale capture from a
// previous target is never shown as if it were this one's.
func (t Terminal) Body(theme theme.Theme, sess TermSession, w, h int) string {
	if sess.CompanionName == "" {
		return theme.DimStyle.Render("No session or workspace selected.")
	}
	if t.shown != sess.CompanionName ||
		strings.TrimSpace(ansi.Strip(t.content)) == "" {
		return theme.DimStyle.Render("Starting shell…")
	}
	if !sess.Root {
		return renderCapture(t.content, w, h)
	}
	return theme.DimStyle.Render("repository root") + "\n" +
		renderCapture(t.content, w, h-1)
}

// companionName is the deterministic tmux name of a companion shell: its base
// tmux name — an agent session's, or a workspace's root name — with a _term
// suffix. Deriving it from the stable base keeps it identical across cockpit
// restarts and distinct from the agent session, so the two never collide.
func companionName(base string) string {
	return base + "_term"
}
