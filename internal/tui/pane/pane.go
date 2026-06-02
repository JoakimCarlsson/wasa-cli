// Package pane holds the cockpit's three right-pane feature machines — the live
// preview, the git diff and the companion terminal — extracted from the root
// Model so each owns its own state and lifecycle. The root tui package is the
// container: it decides which session each pane targets, routes the typed
// messages back to the owning machine, and dispatches the active tab's Body for
// rendering. A pane never reaches back into the root, so there is no import
// cycle; panes depend only on the backend seam, the worktree helper and the
// shared theme layer (for theme.Theme).
package pane

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// renderCapture fits a tmux pane capture to a w×h area for the Preview and
// Terminal tabs: it expands tabs, keeps the last h lines so the freshest output
// shows, and truncates each line to the visible width without slicing an escape
// sequence — resetting at the end so an unterminated colour cannot bleed into
// the pane border or the padding lipgloss adds. The capture is already styled,
// so it is emitted as-is and never re-styled.
func renderCapture(content string, w, h int) string {
	lines := strings.Split(strings.ReplaceAll(content, "\t", "    "), "\n")
	if len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	for i, ln := range lines {
		lines[i] = ansi.Truncate(ln, w, "") + "\x1b[0m"
	}
	return strings.Join(lines, "\n")
}
