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
	th      Theme
	light   [3]int
	dark    [3]int
	variant int // 0 = light, 1 = dark
	channel int // 0 = R, 1 = G, 2 = B

	lastKey string // last key pressed, for repeat acceleration
	repeat  int    // consecutive presses of lastKey
}

func newColorEditor(th Theme, c config.Color) colorEditor {
	return colorEditor{th: th, light: parseRGB(c.Light), dark: parseRGB(c.Dark)}
}

func (e colorEditor) update(key tea.KeyMsg) colorEditor {
	k := key.String()
	if k == e.lastKey {
		e.repeat++
	} else {
		e.repeat = 0
	}
	e.lastKey = k

	switch k {
	case "left":
		e.adjust(-accelStep(e.repeat))
	case "right":
		e.adjust(accelStep(e.repeat))
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

// accelStep grows the adjustment step as a direction key is held: a terminal
// reports a held key as a stream of repeats, so the longer the streak the larger
// the step, letting a value sweep its full range quickly while a single tap still
// nudges by one.
func accelStep(repeat int) int {
	switch {
	case repeat < 3:
		return 1
	case repeat < 8:
		return 5
	default:
		return 15
	}
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
			return e.th.focusedLabelStyle.Render("● " + name)
		}
		return e.th.dimStyle.Render("  " + name)
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
		active := i == e.channel
		marker := "  "
		label := e.th.dimStyle.Render(name)
		value := e.th.dimStyle.Render(pad(strconv.Itoa(cur[i]), 3))
		if active {
			marker = e.th.focusedLabelStyle.Render("▸ ")
			label = e.th.focusedLabelStyle.Render(name)
			value = e.th.focusedLabelStyle.Render(pad(strconv.Itoa(cur[i]), 3))
		}
		rows = append(rows, fmt.Sprintf(
			"%s%s %s %s", marker, label, channelBar(e.th, cur[i], active), value,
		))
	}

	return lipgloss.JoinVertical(
		lipgloss.Left,
		e.th.titleStyle.Render(label),
		"",
		header,
		"",
		strings.Join(rows, "\n"),
	)
}

// channelBar renders a 0–255 channel value as a 24-cell filled bar. The active
// channel's fill uses the accent so the focused row reads at a glance; inactive
// rows are dimmed.
func channelBar(th Theme, v int, active bool) string {
	const n = 24
	filled := v * n / 255
	fill := th.dimStyle
	if active {
		fill = th.matchStyle
	}
	return fill.Render(strings.Repeat("█", filled)) +
		th.dimStyle.Render(strings.Repeat("░", n-filled))
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
