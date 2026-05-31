package backend

import "github.com/joakimcarlsson/wasa/internal/tmux"

// Default returns the session backend for the host platform. Every platform
// currently drives tmux; the Windows-native backend swaps in here behind a
// build tag without changing any caller.
func Default() SessionBackend {
	return tmux.New()
}
