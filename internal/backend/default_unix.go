//go:build !windows

package backend

import tmux "github.com/joakimcarlsson/wasa/internal/backend/unix"

// Default returns the session backend for the host platform. On Linux and macOS
// that is the tmux backend.
func Default() SessionBackend {
	return tmux.New()
}

var _ SessionBackend = (*tmux.Client)(nil)
