package pane

import (
	"charm.land/lipgloss/v2"

	"github.com/joakimcarlsson/wasa-cli/internal/backend"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/component"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/layout"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/theme"
)

// Tab selects which view the right pane shows: the natively drawn session
// overview (the default), a git diff of the session's work, the live capture of
// the agent's own screen, or a companion shell. Only the active tab does
// per-tick work; the others are idle, so cycling away from Preview tears its
// stream down and cycling back resumes it.
//
// Overview leads the strip because it is the one tab wasa draws itself, out of
// facts it owns, at the cockpit's own typography — where Preview can only
// reproduce another program's screen inside a box.
type Tab int

// The right-pane tabs, in strip order.
const (
	TabOverview Tab = iota
	TabDiff
	TabPreview
	TabTerminal
)

// tabNames is the tab strip's labels in Tab order.
var tabNames = [...]string{"Overview", "Diff", "Screen", "Terminal"}

// tabRowRows is the height the tab row occupies above the content: the label
// line and the rule under it.
const tabRowRows = layout.PaneTabRows

// Tabbed composes the right pane's tab strip with the three feature panes and
// tracks which one is active. The Preview, Diff and Terminal panes are exported
// so the root container can drive their lifecycle (targeting, message routing,
// teardown) while Tabbed owns the tab selection and the framed rendering.
type Tabbed struct {
	Overview Overview
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
		Overview: NewOverview(),
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
//
// The pane draws no border, so the body is rendered at exactly contentW by the
// rows left under the tab strip and then held to that height: Height() only
// pads short content, never truncates, so a body that overran would push the
// footer off the screen.
func (t Tabbed) Body(
	th theme.Theme,
	contentW, bodyH int,
	previewRunning bool,
	overviewSess OverviewSession,
	diffSess DiffSession,
	termSess TermSession,
) string {
	windowH := max(bodyH-tabRowRows, 1)

	row := component.TabStrip(th, tabNames[:], int(t.active), contentW)
	window := th.PaneWindowStyle.Width(contentW).
		Height(windowH).
		Render(component.Clamp(
			t.body(
				th, contentW, windowH,
				previewRunning, overviewSess, diffSess, termSess,
			),
			windowH,
		))
	return lipgloss.JoinVertical(lipgloss.Left, row, window)
}

// body renders the body of the active tab into a w×h area.
func (t Tabbed) body(
	th theme.Theme,
	w, h int,
	previewRunning bool,
	overviewSess OverviewSession,
	diffSess DiffSession,
	termSess TermSession,
) string {
	switch t.active {
	case TabDiff:
		return t.Diff.Body(th, diffSess, w, h)
	case TabTerminal:
		return t.Terminal.Body(th, termSess, w, h)
	case TabPreview:
		if !diffSess.Selected {
			return th.DimStyle.Render("No session selected.")
		}
		return t.Preview.Body(th, previewRunning, w, h)
	default:
		return t.Overview.Body(th, overviewSess, w, h)
	}
}
