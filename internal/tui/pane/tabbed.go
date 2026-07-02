package pane

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/joakimcarlsson/wasa-cli/internal/backend"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/component"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/theme"
)

// Tab selects which view the right pane shows: the live preview (the default),
// a git diff of the session's work, or a companion shell. Only the active tab
// does per-tick work; the others are idle, so cycling away from Preview tears
// its stream down and cycling back resumes it.
type Tab int

// The right-pane tabs, in strip order.
const (
	TabPreview Tab = iota
	TabDiff
	TabTerminal
)

// tabNames is the tab strip's labels in Tab order.
var tabNames = [...]string{"Preview", "Diff", "Terminal"}

// tabRowRows is the height the tab row occupies above the content window: the
// box top border, the label line and the bottom edge that doubles as the
// window's top border.
const tabRowRows = 3

// Tabbed composes the right pane's tab strip with the three feature panes and
// tracks which one is active. The Preview, Diff and Terminal panes are exported
// so the root container can drive their lifecycle (targeting, message routing,
// teardown) while Tabbed owns the tab selection and the framed rendering.
type Tabbed struct {
	Preview  Preview
	Diff     Diff
	Terminal Terminal
	active   Tab
}

// NewTabbed builds the three right-pane machines: the live preview over the
// streaming capability and session backend, the diff over the resolved theme,
// and the companion terminal.
func NewTabbed(
	stream backend.StreamingBackend,
	tmux backend.SessionBackend,
	t theme.Theme,
) Tabbed {
	return Tabbed{
		Preview:  NewPreview(stream, tmux),
		Diff:     NewDiff(t),
		Terminal: NewTerminal(),
	}
}

// Cycle advances the active tab by delta, wrapping. Switching away from Preview
// is what lets the root tear the preview stream down; switching back resumes it.
func (t *Tabbed) Cycle(delta int) {
	n := len(tabNames)
	t.active = Tab(((int(t.active)+delta)%n + n) % n)
}

// Active is the tab currently shown.
func (t Tabbed) Active() Tab { return t.active }

// Body renders the right pane: a tab strip across the top sitting on a content
// window that holds the active tab's body. contentW and bodyH are the content
// width and the full body height the pane must fill so it lines up with the
// sessions pane. previewRunning reports whether the previewed session is
// running, and diffSess/termSess project the selection into the facts the Diff
// and Terminal bodies need. The no-session gating for the Preview tab stays in
// the root, which passes a zero-value diffSess/termSess (Selected false) when
// nothing is selected.
func (t Tabbed) Body(
	th theme.Theme,
	contentW, bodyH int,
	previewRunning bool,
	diffSess DiffSession,
	termSess TermSession,
) string {
	contentH := max(bodyH-(tabRowRows-1), 1)

	row := component.TabStrip(th, tabNames[:], int(t.active), contentW+2)
	window := th.PaneWindowStyle.Width(contentW).Height(contentH).Render(
		t.body(th, contentW, contentH, previewRunning, diffSess, termSess),
	)
	return lipgloss.JoinVertical(lipgloss.Left, row, window)
}

// body renders the body of the active tab into a w×h area.
func (t Tabbed) body(
	th theme.Theme,
	w, h int,
	previewRunning bool,
	diffSess DiffSession,
	termSess TermSession,
) string {
	switch t.active {
	case TabDiff:
		return t.Diff.Body(th, diffSess, w, h)
	case TabTerminal:
		return t.Terminal.Body(th, termSess, w, h)
	default:
		if !diffSess.Selected {
			return th.DimStyle.Render("No session selected.")
		}
		return t.Preview.Body(th, previewRunning, w, h)
	}
}
