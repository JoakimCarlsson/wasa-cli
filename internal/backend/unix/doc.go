//go:build !windows

// Package tmux drives the tmux binary: it spawns detached sessions, attaches to
// them, reports whether a session exists, lists sessions and kills them. tmux
// owns the PTYs, terminal emulation and session persistence; this package is the
// thin orchestration seam above it. It shells out to the tmux binary rather than
// speaking the control protocol, assumes the default shared tmux server, and
// surfaces tmux stderr on failure.
package tmux
