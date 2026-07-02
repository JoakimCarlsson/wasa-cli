// Package pane holds the cockpit's three right-pane feature machines — the live
// preview, the git diff and the companion terminal — extracted from the root
// Model so each owns its own state and lifecycle. The root tui package is the
// container: it decides which session each pane targets, routes the typed
// messages back to the owning machine, and dispatches the active tab's Body for
// rendering. A pane never reaches back into the root, so there is no import
// cycle; panes depend only on the backend seam, the worktree helper, the shared
// theme layer (for theme.Theme) and the component layer (for the tab strip the
// Tabbed container frames).
package pane
