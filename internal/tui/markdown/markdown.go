package markdown

import (
	"strings"
	"sync"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2/compat"
	"github.com/charmbracelet/x/ansi"
)

// minWidth is the narrowest column worth laying markdown out in. Below it the
// renderer's own indents and rules leave no room for content, so Render falls
// back to the raw text.
const minWidth = 24

// renderers memoises one Glamour renderer per wrap width. Building a renderer
// parses a stylesheet and constructs a goldmark pipeline; a resize would
// otherwise pay that on every frame.
var renderers sync.Map

// Render lays out markdown source wrapped to width, returning styled terminal
// text with no trailing blank lines. Anything that cannot be rendered — a width
// too narrow to lay out in, a Glamour failure — comes back as the source text
// unchanged, because a transcript is worth showing plainly rather than not at
// all.
func Render(src string, width int) string {
	if strings.TrimSpace(src) == "" || width < minWidth {
		return src
	}
	r, err := rendererFor(width)
	if err != nil {
		return src
	}
	out, err := r.Render(src)
	if err != nil {
		return src
	}
	return strings.Trim(out, "\n")
}

// RenderIndented renders markdown and indents every line by n spaces, the shape
// the transcript uses to set a message's body under its role header.
func RenderIndented(src string, width, n int) string {
	pad := strings.Repeat(" ", n)
	body := Render(src, width-n)
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if strings.TrimSpace(ansi.Strip(l)) == "" {
			continue
		}
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}

// rendererFor returns the cached renderer for a wrap width, building it on
// first use. The style follows the terminal background so the rendered prose
// sits on the same light/dark footing as the rest of the cockpit.
func rendererFor(width int) (*glamour.TermRenderer, error) {
	if cached, ok := renderers.Load(width); ok {
		return cached.(*glamour.TermRenderer), nil
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(standardStyle()),
		glamour.WithWordWrap(width),
		glamour.WithEmoji(),
	)
	if err != nil {
		return nil, err
	}
	renderers.Store(width, r)
	return r, nil
}

// standardStyle is the Glamour stylesheet matching the terminal background.
func standardStyle() string {
	if compat.HasDarkBackground {
		return "dark"
	}
	return "light"
}
