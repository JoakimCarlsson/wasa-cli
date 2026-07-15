package tui

import (
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/joakimcarlsson/wasa-cli/internal/registry"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/component"
)

// The filter's status tokens. A leading "running", "exited" or "paused" word
// narrows the list to that liveness state; the rest of the query is fuzzy text.
// These are the only three tokens — the richer activity states (waiting/idle)
// depend on the live-status work and are not invented here.
const (
	tokenRunning = "running"
	tokenExited  = "exited"
	tokenPaused  = "paused"
)

// filterState is the cockpit's transient session filter: a one-line fuzzy query
// over the active workspace's sessions. It is a view, never a mutation — the
// registry is untouched; sessions() narrows what every list operation reads while
// active is set, and esc tears it down.
type filterState struct {
	active bool
	input  textinput.Model
}

// parseFilterQuery splits a raw filter query into an optional leading status
// token and the remaining fuzzy text. The first whitespace-delimited word, when
// it is exactly "running", "exited" or "paused", selects that liveness state
// and the rest is the fuzzy text; otherwise the whole query is fuzzy text and
// no status token applies. Matching is case-insensitive.
func parseFilterQuery(raw string) (status, text string) {
	raw = strings.TrimSpace(raw)
	first, rest, _ := strings.Cut(raw, " ")
	switch strings.ToLower(first) {
	case tokenRunning, tokenExited, tokenPaused:
		return strings.ToLower(first), strings.TrimSpace(rest)
	default:
		return "", raw
	}
}

// sessionHaystack is the text the fuzzy filter matches a session against: its
// display title, ref (branch, or working-dir basename for a plain session) and
// working-directory basename, joined so a query can hit any of them.
func sessionHaystack(s *registry.Session) string {
	title, ref := sessionLabel(s)
	return title + " " + ref + " " + filepath.Base(s.WorkingDir)
}

// matchesFilter reports whether s passes a parsed filter: the status token,
// when set, must match its liveness exactly — paused is its own state, so
// "exited" no longer means "anything not running" — and the fuzzy text, when
// set, must be a subsequence of its haystack. An all-empty filter matches every
// session.
func matchesFilter(s *registry.Session, status, text string) bool {
	switch status {
	case tokenRunning:
		if s.Status != registry.StatusRunning {
			return false
		}
	case tokenExited:
		if s.Status != registry.StatusExited {
			return false
		}
	case tokenPaused:
		if s.Status != registry.StatusPaused {
			return false
		}
	}
	if text == "" {
		return true
	}
	_, _, ok := component.FuzzyScore(text, sessionHaystack(s))
	return ok
}

// enterFilter opens the session filter over the list, focusing a fresh one-line
// query input and resetting the cursor to the first match. It is a no-op when the
// active workspace has no sessions to filter.
func (m Model) enterFilter() (tea.Model, tea.Cmd) {
	if len(m.workspaceSessions()) == 0 {
		return m, nil
	}
	in := textinput.New()
	in.Prompt = "> "
	in.Placeholder = "filter — prefix running/exited/paused"
	in.CharLimit = 200
	in.SetWidth(max(m.listColWidth()-4, 10))
	in.Focus()

	m.filter = filterState{active: true, input: in}
	m.cursor = 0
	return m, textinput.Blink
}

// exitFilter tears the filter down and restores the full list with the cursor on
// the first session. It re-targets the preview at whatever is now selected.
func (m Model) exitFilter() (tea.Model, tea.Cmd) {
	m.filter = filterState{}
	m.cursor = 0
	return m, m.afterListChange()
}

// updateFilter routes input while the filter is open. esc closes it; enter
// attaches to the highlighted (filtered) session as in the list; up/down move the
// cursor within the filtered set; ctrl+c still quits. Every other key edits the
// query, after which the cursor is clamped into the narrowed set and the preview
// re-targeted so the live view tracks the new selection.
func (m Model) updateFilter(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		var cmd tea.Cmd
		m.filter.input, cmd = m.filter.input.Update(msg)
		return m, cmd
	}

	switch key.String() {
	case "esc":
		return m.exitFilter()
	case "ctrl+c":
		m.tabbed.Preview.Close()
		m.tabbed.Terminal.Close(m.tmux)
		return m, tea.Quit
	case "enter":
		return m.attach()
	case "up", "ctrl+p":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, m.afterListChange()
	case "down", "ctrl+n":
		if m.cursor < len(m.sessions())-1 {
			m.cursor++
		}
		return m, m.afterListChange()
	}

	var cmd tea.Cmd
	m.filter.input, cmd = m.filter.input.Update(msg)
	m.clampCursor()
	return m, tea.Batch(cmd, m.afterListChange())
}

// clampCursor pins the cursor into the current (possibly filtered) session set,
// landing it on a real row after the set shrinks under it.
func (m *Model) clampCursor() {
	if n := len(m.sessions()); m.cursor >= n {
		m.cursor = n - 1
	}
	m.cursor = max(m.cursor, 0)
}
