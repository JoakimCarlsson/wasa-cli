package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/joakimcarlsson/wasa/internal/config"
)

// colorEditor edits one theme colour with RGB sliders. A colour carries a light
// and a dark variant; the editor edits one variant at a time (tab toggles) with
// three channel sliders, and previews both variants live as swatches. It emits a
// "#hex" value (or "#light / #dark" when the variants differ) on commit, which the
// config editor parses back through parseColor.
type colorEditor struct {
	light   [3]int
	dark    [3]int
	variant int // 0 = light, 1 = dark
	channel int // 0 = R, 1 = G, 2 = B
}

func newColorEditor(c config.Color) colorEditor {
	return colorEditor{light: parseRGB(c.Light), dark: parseRGB(c.Dark)}
}

func (e colorEditor) update(key tea.KeyMsg) colorEditor {
	switch key.String() {
	case "left":
		e.adjust(-1)
	case "right":
		e.adjust(1)
	case "[":
		e.adjust(-16)
	case "]":
		e.adjust(16)
	case "tab", "shift+tab":
		e.variant ^= 1
	case "up", "k":
		e.channel = (e.channel + 2) % 3
	case "down", "j":
		e.channel = (e.channel + 1) % 3
	}
	return e
}

func (e *colorEditor) adjust(delta int) {
	ch := e.active()
	ch[e.channel] = clamp(ch[e.channel]+delta, 0, 255)
}

func (e *colorEditor) active() *[3]int {
	if e.variant == 1 {
		return &e.dark
	}
	return &e.light
}

// value renders the edited colour for commit: a single hex when both variants
// match, "light / dark" hex when they differ.
func (e colorEditor) value() string {
	l, d := hexOf(e.light), hexOf(e.dark)
	if l == d {
		return l
	}
	return l + " / " + d
}

func (e colorEditor) view(label string) string {
	swatch := func(rgb [3]int) string {
		return lipgloss.NewStyle().
			Background(lipgloss.Color(hexOf(rgb))).
			Render("      ")
	}
	variantTag := func(name string, v int) string {
		if e.variant == v {
			return focusedLabelStyle.Render("● " + name)
		}
		return dimStyle.Render("  " + name)
	}

	header := fmt.Sprintf(
		"%s  %s    %s  %s",
		variantTag("light", 0), swatch(e.light),
		variantTag("dark", 1), swatch(e.dark),
	)

	names := [3]string{"R", "G", "B"}
	cur := *e.active()
	var rows []string
	for i, name := range names {
		label := name
		if i == e.channel {
			label = focusedLabelStyle.Render(name)
		} else {
			label = dimStyle.Render(label)
		}
		rows = append(rows, fmt.Sprintf(
			"%s %s %s", label, channelBar(cur[i]), pad(strconv.Itoa(cur[i]), 3),
		))
	}

	return lipgloss.JoinVertical(
		lipgloss.Left,
		titleStyle.Render(label),
		"",
		header,
		"",
		strings.Join(rows, "\n"),
	)
}

// channelBar renders a 0–255 channel value as a 24-cell filled bar.
func channelBar(v int) string {
	const n = 24
	filled := v * n / 255
	return matchStyle.Render(strings.Repeat("█", filled)) +
		dimStyle.Render(strings.Repeat("░", n-filled))
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func hexOf(rgb [3]int) string {
	return fmt.Sprintf("#%02x%02x%02x", rgb[0], rgb[1], rgb[2])
}

// parseRGB resolves a stored colour value to RGB for editing. It accepts #rgb and
// #rrggbb hex and an ANSI-256 index (converted to its RGB), falling back to mid
// grey for anything it cannot read so the sliders still open on a sane value.
func parseRGB(s string) [3]int {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "#") {
		if rgb, ok := parseHex(s[1:]); ok {
			return rgb
		}
		return [3]int{128, 128, 128}
	}
	if n, err := strconv.Atoi(s); err == nil && n >= 0 && n <= 255 {
		return ansi256RGB(n)
	}
	return [3]int{128, 128, 128}
}

func parseHex(h string) ([3]int, bool) {
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		return [3]int{}, false
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return [3]int{}, false
	}
	return [3]int{int(v >> 16 & 0xff), int(v >> 8 & 0xff), int(v & 0xff)}, true
}

// ansi256Base holds the RGB of the 16 standard ANSI colours, which do not follow
// the cube/grayscale formula used for indices 16–255.
var ansi256Base = [16][3]int{
	{0, 0, 0}, {128, 0, 0}, {0, 128, 0}, {128, 128, 0},
	{0, 0, 128}, {128, 0, 128}, {0, 128, 128}, {192, 192, 192},
	{128, 128, 128}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0},
	{0, 0, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
}

// ansi256RGB converts an ANSI-256 colour index to its RGB triple: the 16 base
// colours from a table, the 6×6×6 cube (16–231) and the grayscale ramp (232–255)
// from their standard formulae.
func ansi256RGB(n int) [3]int {
	switch {
	case n < 16:
		return ansi256Base[n]
	case n < 232:
		steps := [6]int{0, 95, 135, 175, 215, 255}
		c := n - 16
		return [3]int{steps[c/36%6], steps[c/6%6], steps[c%6]}
	default:
		v := 8 + 10*(n-232)
		return [3]int{v, v, v}
	}
}
