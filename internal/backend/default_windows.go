//go:build windows

package backend

import conpty "github.com/joakimcarlsson/wasa/internal/backend/windows"

// Default returns the session backend for the host platform. On Windows that is
// the native pseudo-console (ConPTY) backend, which needs no tmux binary and no
// WSL; it auto-starts a background daemon that owns the sessions.
func Default() SessionBackend {
	return conpty.New()
}

var _ SessionBackend = (*conpty.Client)(nil)
