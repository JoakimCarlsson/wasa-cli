//go:build !windows

package backend

import tmux "github.com/joakimcarlsson/wasa-cli/internal/backend/unix"

// Default returns the session backend for the host platform. On Linux and macOS
// that is the tmux backend, wrapped so its control-mode Watch satisfies the
// StreamingBackend capability the TUI prefers for the live preview.
func Default() SessionBackend {
	return unixBackend{tmux.New()}
}

// unixBackend adapts the tmux client to the backend seam. It embeds the client
// for the core SessionBackend surface and adapts Watch's concrete return type
// to backend.Watcher, which keeps the StreamingBackend interface in this package
// without the unix package importing it (which would cycle through Default).
type unixBackend struct{ *tmux.Client }

// Watch opens a control-mode stream of the named session's pane content. The
// concrete *tmux.ControlConn it returns satisfies Watcher.
func (u unixBackend) Watch(name string) (Watcher, error) {
	return u.Client.Watch(name)
}

var (
	_ SessionBackend   = unixBackend{}
	_ StreamingBackend = unixBackend{}
)
