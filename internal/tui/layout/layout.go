package layout

import "github.com/joakimcarlsson/wasa-cli/internal/config"

// The spacing scale. Every indent, gap and pad in the cockpit is one of these
// rather than a literal, so the rhythm is consistent across panes and can be
// changed in one place. Step is the base unit; the names are what callers use.
const (
	Step  = 1
	Tight = Step     // inside a token: glyph to label
	Snug  = Step * 2 // between related items on a line
	Loose = Step * 3 // between sections in a pane
)

// The chrome the full-bleed frame spends on itself, in terminal rows. They are
// listed separately rather than as one constant so a change to the frame is a
// change to the part that moved: the workspace tab strip, the blank row that
// separates it from the body, the footer hint bar and the status line under
// it. Nothing is spent on borders — the cockpit draws no boxes, letting the
// body run to the edges of the terminal the way the shell around it does.
const (
	TabRow    = 1
	TabGap    = 1
	MenuRow   = 1
	StatusRow = 1
)

// ChromeRows is the total height the frame takes before any content: what a
// view must subtract from the terminal height to get its body height.
const ChromeRows = TabRow + TabGap + MenuRow + StatusRow

// PaneGutter is the width of the divider column between the two body panes: a
// vertical rule with a space either side.
const PaneGutter = 3

// PaneTabRows is the height the right pane's tab strip occupies above its
// body: the label row and the rule under it.
const PaneTabRows = 2

// MinBodyRows is the shortest body the two-column frame will claim to have, so
// a squeezed terminal still renders a row rather than a negative-height pane.
const MinBodyRows = 3

// Frame is the resolved geometry of the cockpit's frame for one terminal size:
// whether the compact single-column fallback applies, how tall the body is, and
// how the width splits between the list and the right pane. Views ask for a
// Frame instead of doing the arithmetic, so the sessions list, the checkpoints
// browser and the panes cannot drift out of alignment with each other.
type Frame struct {
	Width  int
	Height int

	// Compact reports that the terminal is below the configured thresholds and
	// the single-column fallback should be drawn instead.
	Compact bool

	// Body is the height available to the body panes, between the tab strip and
	// the footer.
	Body int

	// List and Right are the widths of the two body columns. With no borders
	// drawn they are content widths: what a pane may fill, edge to edge.
	List  int
	Right int
}

// New resolves the frame for a terminal of width×height under the configured
// layout thresholds.
func New(l config.Layout, width, height int) Frame {
	f := Frame{
		Width:   width,
		Height:  height,
		Compact: width < l.CompactWidth || height < l.CompactHeight,
		Body:    max(height-ChromeRows, MinBodyRows),
	}
	f.List = max(int(float64(width)*l.ListColFrac), l.MinListWidth)
	f.Right = max(width-f.List-PaneGutter, 1)
	return f
}
