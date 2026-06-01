package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/joakimcarlsson/wasa/internal/backend"
	"github.com/joakimcarlsson/wasa/internal/registry"
	"github.com/joakimcarlsson/wasa/internal/tui/component"
)

// paneTab selects which view the right pane shows: the live preview (today's
// default), a git diff of the session's work, or a companion shell. Only the
// active tab does per-tick work; the others are idle, so cycling away from
// Preview tears its stream down and cycling back resumes it.
type paneTab int

const (
	panePreview paneTab = iota
	paneDiff
	paneTerminal
)

// paneTabNames is the tab strip's labels in paneTab order.
var paneTabNames = [...]string{"Preview", "Diff", "Terminal"}

// tabRowRows is the height the tab row occupies above the content window: the
// box top border, the label line and the bottom edge that doubles as the
// window's top border.
const tabRowRows = 3

// tabbedPane is the right pane: a tab strip over three feature machines —
// preview, diff and terminal — of which only the active one does per-tick work.
// It owns the active tab and delegates retargeting, ticking and rendering to the
// pane that owns each behaviour, so the root model holds one value instead of
// three independent state machines.
type tabbedPane struct {
	active   paneTab
	preview  previewPane
	diff     diffPane
	terminal terminalPane
}

func newTabbedPane(tmux backend.SessionBackend, stream backend.StreamingBackend) tabbedPane {
	return tabbedPane{
		preview:  newPreviewPane(tmux, stream),
		diff:     newDiffPane(),
		terminal: newTerminalPane(tmux),
	}
}

// cycle advances the active tab by delta, wrapping. The caller then re-runs
// retarget, which tears the preview stream down when the new tab is not Preview
// and re-establishes it on return.
func (t *tabbedPane) cycle(delta int) {
	n := len(paneTabNames)
	t.active = paneTab(((int(t.active)+delta)%n + n) % n)
}

func (t *tabbedPane) setSize(w, h int) { t.diff.setSize(w, h) }

// previewTarget is the tmux name the preview should track: the selected
// session's, or "" when nothing running is selected or the Preview tab is not
// active. Gating on the active tab is what keeps the streaming preview's cost
// off the other tabs.
func (t tabbedPane) previewTarget(s *registry.Session) string {
	if t.active != panePreview {
		return ""
	}
	if s == nil || s.Status != registry.StatusRunning {
		return ""
	}
	return s.TmuxName
}

// retarget runs after a selection or tab change: it re-targets the preview
// stream (tearing it down off the Preview tab) and, on the Terminal or Diff tab,
// kicks an immediate companion ensure or diff compute so switching to it or
// moving the cursor shows the body without waiting for the next tick.
func (t *tabbedPane) retarget(s *registry.Session, reg *registry.Registry, home string) tea.Cmd {
	cmd := t.preview.retarget(t.previewTarget(s))
	switch t.active {
	case paneTerminal:
		cmd = tea.Batch(cmd, t.terminal.ensure(s))
	case paneDiff:
		cmd = tea.Batch(cmd, t.diff.ensure(s, reg, home))
	}
	return cmd
}

// retargetPreview re-targets only the preview stream at s, tearing it down when
// the Preview tab is not active. It is the lighter counterpart to retarget used
// after a refresh or when leaving the config panel, where the active tab's body
// is recomputed by the next tick rather than immediately.
func (t *tabbedPane) retargetPreview(s *registry.Session) tea.Cmd {
	return t.preview.retarget(t.previewTarget(s))
}

// tick is the per-tick work for the active tab. The Preview and Diff tabs
// poll-or-reconnect the preview stream — a near no-op off the Preview tab, since
// previewTarget is then empty — while the Terminal tab ensures the selected
// session's companion shell exists and re-captures it.
func (t *tabbedPane) tick(s *registry.Session) tea.Cmd {
	if t.active == paneTerminal {
		return t.terminal.ensure(s)
	}
	return t.preview.pollOrReconnect(t.previewTarget(s))
}

// apply routes a pane message to the pane that owns it and returns any follow-up
// command. A terminal error is surfaced to the caller through err.
func (t *tabbedPane) apply(th Theme, msg tea.Msg, s *registry.Session) (cmd tea.Cmd, err error) {
	switch msg := msg.(type) {
	case previewMsg:
		return t.preview.apply(msg), nil
	case termMsg:
		return nil, t.terminal.apply(msg, s)
	case diffMsg:
		t.diff.apply(th, msg, s)
		return nil, nil
	}
	return nil, nil
}

// handleKey forwards a key to the diff viewport while the Diff tab is active, so
// the diff scrolls without disturbing the list bindings.
func (t *tabbedPane) handleKey(msg tea.Msg) {
	if t.active == paneDiff {
		t.diff.handleKey(msg)
	}
}

func (t *tabbedPane) close() { t.preview.close(); t.terminal.close() }

// liveContent exposes the preview's live capture for name so the status sweep
// can reuse the focused session's stream rather than re-capturing it.
func (t tabbedPane) liveContent(name string) (string, bool) {
	return t.preview.liveContent(name)
}

// view renders the right pane as a row of connected tab boxes — Preview, Diff,
// Terminal — sitting on a content window, in the lipgloss tabs idiom (after
// claude-squad): the tabs span the pane width, the active tab's bottom border
// opens into the window beneath it, and the inactive tabs close against the
// window's top edge. contentW and bodyH are the content width and the full body
// height the pane must fill so it lines up with the sessions pane.
func (t tabbedPane) view(th Theme, s *registry.Session, contentW, bodyH int) string {
	contentH := max(bodyH-(tabRowRows-1), 1)

	row := component.Tabs{
		Names:         paneTabNames[:],
		Active:        int(t.active),
		ActiveStyle:   th.paneTabActiveStyle,
		InactiveStyle: th.paneTabInactiveStyle,
	}.Render(contentW + 2)
	window := th.paneWindowStyle.Width(contentW).Height(contentH).Render(
		t.body(th, s, contentW, contentH),
	)
	return lipgloss.JoinVertical(lipgloss.Left, row, window)
}

// body renders the body of the active tab into a w×h area.
func (t tabbedPane) body(th Theme, s *registry.Session, w, h int) string {
	switch t.active {
	case paneDiff:
		return t.diff.view(th, s, w, h)
	case paneTerminal:
		return t.terminal.view(th, s, w, h)
	default:
		return t.preview.view(th, s, w, h)
	}
}
