// Package backend is the session-backend seam: the single interface the
// orchestration layer (launch, the CLI and the TUI) uses to spawn, attach to,
// inspect and tear down persistent terminal sessions, plus the Default selector
// that picks the implementation for the host platform.
//
// Extracting this seam decouples wasa from tmux. Default returns the tmux
// backend (internal/backend/unix), which satisfies SessionBackend, so callers
// depend on the interface and never on a concrete backend type.
package backend
