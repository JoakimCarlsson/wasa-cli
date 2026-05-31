// Package backend is the session-backend seam: the single interface the
// orchestration layer (launch, the CLI and the TUI) uses to spawn, attach to,
// inspect and tear down persistent terminal sessions, plus the Default selector
// that picks the implementation for the host platform.
//
// Extracting this seam decouples wasa from tmux. On Linux and macOS Default
// returns *tmux.Client, which drives the tmux binary. The seam is what lets a
// native Windows backend land as a sibling implementation without touching any
// call site: every caller depends on SessionBackend, never on a concrete
// backend type.
package backend

import (
	"os/exec"

	"github.com/joakimcarlsson/wasa/internal/tmux"
)

// SessionBackend is everything wasa's orchestration layer needs from a session
// multiplexer. It is the minimal surface tmux already provides — detached
// sessions that own their PTYs and outlive the wasa process — and nothing more:
// no splits, panes or multiplexer configuration. A backend persists sessions
// across wasa invocations and addresses them by name.
type SessionBackend interface {
	// SpawnEnv creates a detached session named name running program (an
	// interactive shell when none is given) with working directory dir, with
	// each KEY=VALUE entry of env injected into the session environment.
	SpawnEnv(name, dir string, env []string, program ...string) error

	// AttachCmd returns the unstarted command that attaches to the session
	// named name, with no standard streams wired. The caller owns the terminal
	// for the attach's duration: the TUI hands this command to tea.ExecProcess,
	// which suspends its renderer, runs it against the real terminal and resumes
	// on detach.
	AttachCmd(name string) (*exec.Cmd, error)

	// Capture returns the visible contents of the session's active pane as plain
	// text for a read-only preview. A session that no longer exists yields an
	// empty string rather than an error.
	Capture(name string) (string, error)

	// Has reports whether a session named name exists. A missing session is not
	// an error.
	Has(name string) (bool, error)

	// List returns the names of the live sessions.
	List() ([]string, error)

	// Kill terminates the session named name.
	Kill(name string) error
}

var _ SessionBackend = (*tmux.Client)(nil)
