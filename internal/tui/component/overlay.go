// Package component holds the cockpit's generic, reusable UI pieces — an
// overlay compositor, a tab strip, a themed list and a key/help bar — built on
// bubbles and lipgloss with no knowledge of wasa's domain. The app layer
// composes these with its registry, backend and theme; nothing here imports the
// app, so a component can be tested and reused in isolation. Styling is supplied
// by the caller as lipgloss.Style values, never read from global state.
package component

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Overlay composites fg centered on top of bg and returns the merged frame.
// Both are treated as ANSI-styled, width-aware blocks of lines: each foreground
// line replaces only the cells it covers on its background row, so the
// background stays visible around the box instead of being cleared — a modal
// floating over the content rather than a full-screen swap. The cuts go through
// x/ansi so a style is never sliced mid-escape.
func Overlay(fg, bg string) string {
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
