package component

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

// Pad truncates s to w visible cells (appending an ellipsis when it had to cut)
// and then right-pads it back to exactly w, so a rendered row fills its column
// width without wrapping. A non-positive w returns s unchanged.
func Pad(s string, w int) string {
	if w <= 0 {
		return s
	}
	s = runewidth.Truncate(s, w, "…")
	if gap := w - runewidth.StringWidth(s); gap > 0 {
		s += strings.Repeat(" ", gap)
	}
	return s
}

// fitColumn pads every line to exactly w visible cells and the block to exactly
// height lines, so columns align when joined horizontally.
func fitColumn(lines []string, w, height int) []string {
	out := make([]string, height)
	for i := range out {
		if i < len(lines) {
			out[i] = fitAnsi(lines[i], w)
		} else {
			out[i] = strings.Repeat(" ", w)
		}
	}
	return out
}

// fitAnsi pads or truncates an ANSI-styled string to exactly w visible cells.
func fitAnsi(s string, w int) string {
	vis := ansi.StringWidth(s)
	if vis > w {
		return ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", w-vis)
}

// tailTrunc keeps the rightmost w visible cells of a plain string, prefixing an
// ellipsis when it had to cut — so a path keeps its tail, the directory name.
func tailTrunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	vis := ansi.StringWidth(s)
	if vis <= w {
		return s
	}
	return ansi.TruncateLeft(s, vis-(w-1), "…")
}

// PlaceOverlay composites fg centered on top of bg and returns the merged frame.
// Both are treated as ANSI-styled, width-aware blocks of lines: each foreground
// line replaces only the cells it covers on its background row, so the cockpit
// stays visible around the box instead of being cleared — a modal floating over
// the list rather than a full-screen swap. The cuts go through x/ansi so a style
// is never sliced mid-escape.
func PlaceOverlay(fg, bg string) string {
	bgLines := strings.Split(bg, "\n")
	fgLines := strings.Split(fg, "\n")

	bgWidth := blockWidth(bgLines)
	fgWidth := blockWidth(fgLines)

	x := max((bgWidth-fgWidth)/2, 0)
	y := max((len(bgLines)-len(fgLines))/2, 0)

	for i, fgLine := range fgLines {
		row := y + i
		if row >= len(bgLines) {
			break
		}
		bgLine := padLine(bgLines[row], bgWidth)
		left := ansi.Truncate(bgLine, x, "")
		left += strings.Repeat(" ", max(x-ansi.StringWidth(left), 0))
		right := ansi.TruncateLeft(bgLine, x+ansi.StringWidth(fgLine), "")
		bgLines[row] = left + "\x1b[0m" + fgLine + "\x1b[0m" + right
	}
	return strings.Join(bgLines, "\n")
}

// blockWidth is the widest visible line in a block.
func blockWidth(lines []string) int {
	w := 0
	for _, l := range lines {
		if lw := ansi.StringWidth(l); lw > w {
			w = lw
		}
	}
	return w
}

// padLine right-pads s with spaces to w visible cells so a column index lands at
// the same place on every row when overlaying.
func padLine(s string, w int) string {
	if gap := w - ansi.StringWidth(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}
