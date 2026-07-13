package backend

import "os/exec"

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

	// Capture returns the visible contents of the session's active pane, with
	// the pane's escape sequences preserved, for a read-only preview that keeps
	// the session's colors. A session that no longer exists yields an empty
	// string rather than an error.
	Capture(name string) (string, error)

	// Has reports whether a session named name exists. A missing session is not
	// an error.
	Has(name string) (bool, error)

	// List returns the names of the live sessions.
	List() ([]string, error)

	// Kill terminates the session named name.
	Kill(name string) error
}

// ExitReporter is the optional capability a SessionBackend may implement to
// report a finished session's exit status, so the orchestration layer can tell a
// session that finished cleanly from one that failed. A backend that does not
// implement it still reports liveness through Has, and reconcile falls back to
// marking such sessions exited with no captured code.
type ExitReporter interface {
	// PaneExit reports whether the named session's program is still running and,
	// once it has exited, its exit code when the backend recorded one. alive is
	// true while the program runs; when it is false, exitCode is the program's
	// status or nil when unknown (killed outright or died on a signal). A missing
	// session reports (false, nil, nil).
	PaneExit(name string) (alive bool, exitCode *int, err error)
}

// ExitProbe returns the liveness-and-exit probe registry Reconcile uses to mark
// finished sessions. When be reports exit codes it surfaces them; otherwise it
// falls back to Has and reports no code, so every backend reconciles correctly
// and only exit-aware ones distinguish finished from failed.
func ExitProbe(
	be SessionBackend,
) func(name string) (alive bool, exitCode *int, err error) {
	if er, ok := be.(ExitReporter); ok {
		return er.PaneExit
	}
	return func(name string) (bool, *int, error) {
		alive, err := be.Has(name)
		return alive, nil, err
	}
}

// StreamingBackend is the optional capability a SessionBackend may implement to
// deliver live, event-driven preview updates over a single persistent
// connection instead of re-capturing the pane on a fixed poll. The TUI prefers
// Watch when a backend provides it and falls back to the one-shot Capture poll
// otherwise, so a backend that does not implement this interface keeps
// previewing via the Capture poll.
type StreamingBackend interface {
	// Watch opens a live subscription to the named session's pane content and
	// returns a Watcher streaming it. The caller owns the returned Watcher and
	// must Close it when the session stops being previewed. An error means the
	// stream could not be opened; the caller should fall back to Capture.
	Watch(name string) (Watcher, error)
}

// Watcher is a live subscription to a session's pane content. Updates yields a
// fresh full-pane capture, with escape sequences preserved (like Capture),
// whenever the pane changes; it never repeats a byte-identical capture, so an
// idle session produces no values. The channel is closed when the watch ends —
// because Close was called or the underlying connection dropped — which a
// consumer can use to fall back to polling. Close tears the subscription down
// and is safe to call more than once.
type Watcher interface {
	Updates() <-chan string
	Close() error
}
