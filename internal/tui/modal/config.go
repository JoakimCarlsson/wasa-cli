package modal

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/joakimcarlsson/wasa-cli/internal/config"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/component"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/theme"
)

// ConfigApplyMsg is emitted by a ConfigEditor when a field is committed; the
// cockpit persists the working config and applies it live.
type ConfigApplyMsg struct{}

// ConfigCloseMsg is emitted by a ConfigEditor when the user leaves the panel.
type ConfigCloseMsg struct{}

func configApply() tea.Msg { return ConfigApplyMsg{} }

func configClose() tea.Msg { return ConfigCloseMsg{} }

// fieldKind selects how a setting is edited: a free-text input for numbers, the
// RGB slider picker for colours, or the press-to-record capture for key bindings.
type fieldKind int

const (
	kindText fieldKind = iota
	kindColor
	kindKeys
)

// editPhase is the editor's current sub-state: browsing the field list, or inside
// one of the per-kind editors for the focused field.
type editPhase int

const (
	editNone editPhase = iota
	editText
	editColor
	editKeys
)

// cfgField is one editable setting in the config panel: a labelled value under a
// section, with a getter that renders the working value as text and a setter that
// parses an edited value back into the working config (returning a parse error the
// panel shows inline). kind selects which sub-editor opens on it.
type cfgField struct {
	section string
	label   string
	kind    fieldKind
	get     func(c config.Config) string
	set     func(c *config.Config, s string) error
}

// ConfigEditor is the in-cockpit settings panel. It edits a working copy of the
// resolved config — theme colours, key bindings and layout — and on save hands
// that copy back to the cockpit, which persists it and applies it live. Colours
// are edited with RGB sliders, bindings by recording keypresses, layout values as
// numbers; every edit funnels back through the same string setters as config.json.
type ConfigEditor struct {
	theme   theme.Theme
	working config.Config
	fields  []cfgField
	cursor  int

	phase  editPhase
	input  textinput.Model
	color  colorEditor
	record recordEditor

	err    string
	width  int
	height int
}

// NewConfigEditor builds the settings panel over a working copy of cfg, sized to
// width and height and styled with theme.
func NewConfigEditor(
	theme theme.Theme, cfg config.Config, width, height int,
) ConfigEditor {
	working := cfg
	working.Keys = make(config.Keys, len(cfg.Keys))
	for action, keys := range cfg.Keys {
		cp := make(config.KeyList, len(keys))
		copy(cp, keys)
		working.Keys[action] = cp
	}
	return ConfigEditor{
		theme:   theme,
		working: working,
		fields:  configFields(),
		width:   width,
		height:  height,
	}
}

// configFields enumerates every editable setting, in display order: the theme
// palette, then the key bindings, then the layout values.
func configFields() []cfgField {
	var fs []cfgField

	colors := []struct {
		label string
		ptr   func(*config.Theme) *config.Color
	}{
		{"accent", func(t *config.Theme) *config.Color { return &t.Accent }},
		{"running", func(t *config.Theme) *config.Color { return &t.Running }},
		{"exited", func(t *config.Theme) *config.Color { return &t.Exited }},
		{"title", func(t *config.Theme) *config.Color { return &t.Title }},
		{"desc", func(t *config.Theme) *config.Color { return &t.Desc }},
		{
			"selectionFg",
			func(t *config.Theme) *config.Color { return &t.SelectionFg },
		},
		{
			"selectionBg",
			func(t *config.Theme) *config.Color { return &t.SelectionBg },
		},
		{"danger", func(t *config.Theme) *config.Color { return &t.Danger }},
		{
			"onAccent",
			func(t *config.Theme) *config.Color { return &t.OnAccent },
		},
		{
			"inactiveBtnBg",
			func(t *config.Theme) *config.Color { return &t.InactiveBtnBg },
		},
		{"menuKey", func(t *config.Theme) *config.Color { return &t.MenuKey }},
		{
			"menuDesc",
			func(t *config.Theme) *config.Color { return &t.MenuDesc },
		},
		{"menuSep", func(t *config.Theme) *config.Color { return &t.MenuSep }},
	}
	for _, c := range colors {
		ptr := c.ptr
		fs = append(fs, cfgField{
			section: "Theme",
			label:   c.label,
			kind:    kindColor,
			get: func(cfg config.Config) string {
				return showColor(*ptr(&cfg.Theme))
			},
			set: func(cfg *config.Config, s string) error {
				col, err := parseColor(s)
				if err != nil {
					return err
				}
				*ptr(&cfg.Theme) = col
				return nil
			},
		})
	}

	for _, action := range config.Actions() {
		a := action
		fs = append(fs, cfgField{
			section: "Keys",
			label:   a,
			kind:    kindKeys,
			get: func(cfg config.Config) string {
				return strings.Join(cfg.Keys[a], ", ")
			},
			set: func(cfg *config.Config, s string) error {
				keys, err := parseKeys(s)
				if err != nil {
					return err
				}
				cfg.Keys[a] = keys
				return nil
			},
		})
	}

	fs = append(fs,
		floatField("listColFrac",
			func(l *config.Layout) *float64 { return &l.ListColFrac }),
		intField("minListWidth",
			func(l *config.Layout) *int { return &l.MinListWidth }),
		intField("compactWidth",
			func(l *config.Layout) *int { return &l.CompactWidth }),
		intField("compactHeight",
			func(l *config.Layout) *int { return &l.CompactHeight }),
	)

	fs = append(fs, cfgField{
		section: "Notify",
		label:   "mode",
		kind:    kindText,
		get: func(cfg config.Config) string {
			return string(cfg.Notify)
		},
		set: func(cfg *config.Config, s string) error {
			n := config.Notify(strings.TrimSpace(s))
			if err := config.ValidateNotify(n); err != nil {
				return err
			}
			cfg.Notify = n
			return nil
		},
	})

	fs = append(fs, cfgField{
		section: "History",
		label:   "enabled",
		kind:    kindText,
		get: func(cfg config.Config) string {
			return strconv.FormatBool(cfg.History.Enabled)
		},
		set: func(cfg *config.Config, s string) error {
			v, err := strconv.ParseBool(strings.TrimSpace(s))
			if err != nil {
				return fmt.Errorf("expected true or false, got %q", s)
			}
			cfg.History.Enabled = v
			return nil
		},
	}, cfgField{
		section: "History",
		label:   "maxBytes",
		kind:    kindText,
		get: func(cfg config.Config) string {
			return strconv.Itoa(cfg.History.MaxBytes)
		},
		set: func(cfg *config.Config, s string) error {
			v, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil || v < 0 {
				return fmt.Errorf(
					"expected a non-negative whole number, got %q",
					s,
				)
			}
			cfg.History.MaxBytes = v
			return nil
		},
	})
	return fs
}

func floatField(label string, ptr func(*config.Layout) *float64) cfgField {
	return cfgField{
		section: "Layout",
		label:   label,
		kind:    kindText,
		get: func(cfg config.Config) string {
			return strconv.FormatFloat(*ptr(&cfg.Layout), 'g', -1, 64)
		},
		set: func(cfg *config.Config, s string) error {
			v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
			if err != nil {
				return fmt.Errorf("expected a number, got %q", s)
			}
			*ptr(&cfg.Layout) = v
			return nil
		},
	}
}

func intField(label string, ptr func(*config.Layout) *int) cfgField {
	return cfgField{
		section: "Layout",
		label:   label,
		kind:    kindText,
		get: func(cfg config.Config) string {
			return strconv.Itoa(*ptr(&cfg.Layout))
		},
		set: func(cfg *config.Config, s string) error {
			v, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil {
				return fmt.Errorf("expected a whole number, got %q", s)
			}
			*ptr(&cfg.Layout) = v
			return nil
		},
	}
}

// showColor renders a colour for display: a single value when the light and dark
// variants match, "light / dark" when they differ.
func showColor(c config.Color) string {
	if c.Light == c.Dark {
		return c.Light
	}
	return c.Light + " / " + c.Dark
}

// parseColor parses a colour value. "#hex" (or an ANSI index) sets both variants;
// "light / dark" sets them independently. It is the commit path for both the
// slider editor (which emits hex) and any direct value.
func parseColor(s string) (config.Color, error) {
	parts := strings.Split(s, "/")
	switch len(parts) {
	case 1:
		v := strings.TrimSpace(parts[0])
		if v == "" {
			return config.Color{}, fmt.Errorf("colour cannot be empty")
		}
		return config.Color{Light: v, Dark: v}, nil
	case 2:
		l, d := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if l == "" || d == "" {
			return config.Color{}, fmt.Errorf(`use "#hex" or "light / dark"`)
		}
		return config.Color{Light: l, Dark: d}, nil
	default:
		return config.Color{}, fmt.Errorf(`use "#hex" or "light / dark"`)
	}
}

// parseKeys parses a binding: a comma-separated list of key strings.
func parseKeys(s string) (config.KeyList, error) {
	var keys config.KeyList
	for k := range strings.SplitSeq(s, ",") {
		if k = strings.TrimSpace(k); k != "" {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("bind at least one key")
	}
	return keys, nil
}

// Update routes a message into the panel — the field list or the active
// sub-editor — returning the updated editor and a command. The command emits a
// ConfigApplyMsg when a field is committed or a ConfigCloseMsg when the panel is
// left, and is otherwise the active sub-editor's own command or nil.
func (e ConfigEditor) Update(msg tea.Msg) (ConfigEditor, tea.Cmd) {
	switch e.phase {
	case editText:
		return e.updateText(msg)
	case editColor:
		return e.updateColor(msg)
	case editKeys:
		return e.updateKeys(msg)
	}
	return e.updateList(msg)
}

// updateList routes input for the field list: navigation, opening the focused
// field's editor, save and cancel.
func (e ConfigEditor) updateList(
	msg tea.Msg,
) (ConfigEditor, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return e, nil
	}
	switch key.String() {
	case "esc", "q":
		return e, configClose
	case "up", "k":
		if e.cursor > 0 {
			e.cursor--
		}
	case "down", "j":
		if e.cursor < len(e.fields)-1 {
			e.cursor++
		}
	case "enter":
		return e.beginEdit()
	}
	return e, nil
}

// beginEdit opens the sub-editor for the focused field, chosen by its kind.
func (e ConfigEditor) beginEdit() (ConfigEditor, tea.Cmd) {
	f := e.fields[e.cursor]
	e.err = ""
	switch f.kind {
	case kindColor:
		col, _ := parseColor(f.get(e.working))
		e.color = newColorEditor(e.theme, col)
		e.phase = editColor
		return e, nil
	case kindKeys:
		e.record = newRecordEditor(e.theme, f.label, e.working)
		e.phase = editKeys
		return e, nil
	default:
		e.input = textinput.New()
		e.input.CharLimit = 200
		e.input.SetValue(f.get(e.working))
		e.input.CursorEnd()
		e.input.Focus()
		e.phase = editText
		return e, textinput.Blink
	}
}

func (e ConfigEditor) updateText(
	msg tea.Msg,
) (ConfigEditor, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			return e.commit(e.input.Value())
		case "esc":
			e.phase = editNone
			e.err = ""
			return e, nil
		}
	}
	var cmd tea.Cmd
	e.input, cmd = e.input.Update(msg)
	return e, cmd
}

func (e ConfigEditor) updateColor(
	msg tea.Msg,
) (ConfigEditor, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return e, nil
	}
	switch key.String() {
	case "enter":
		return e.commit(e.color.value())
	case "esc":
		e.phase = editNone
		e.err = ""
		return e, nil
	}
	e.color = e.color.update(key)
	return e, nil
}

func (e ConfigEditor) updateKeys(
	msg tea.Msg,
) (ConfigEditor, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return e, nil
	}
	switch key.String() {
	case "enter":
		return e.commit(e.record.value())
	case "esc":
		e.phase = editNone
		e.err = ""
		return e, nil
	case "backspace", "ctrl+h":
		e.record = e.record.removeLast()
		return e, nil
	}
	e.record = e.record.add(key.String())
	return e, nil
}

// commit parses value into the focused field's setter. A parse error keeps the
// sub-editor open with the message; success returns to the field list and emits
// a ConfigApplyMsg so the cockpit persists and applies the edit.
func (e ConfigEditor) commit(value string) (ConfigEditor, tea.Cmd) {
	if err := e.fields[e.cursor].set(&e.working, value); err != nil {
		e.err = err.Error()
		return e, nil
	}
	e.phase = editNone
	e.err = ""
	return e, configApply
}

// Config returns the editor's working configuration, for the cockpit to persist
// and apply on save.
func (e ConfigEditor) Config() config.Config { return e.working }

// Err is the editor's current inline error message, if any.
func (e ConfigEditor) Err() string { return e.err }

// SetErr sets the editor's inline error message, so the cockpit can surface a
// persist failure on the panel without reopening it.
func (e *ConfigEditor) SetErr(msg string) { e.err = msg }

// View renders the settings panel.
func (e ConfigEditor) View() string {
	var body string
	switch e.phase {
	case editColor:
		body = e.color.view(e.fields[e.cursor].label)
	case editKeys:
		body = e.record.view()
	default:
		body = e.listBody()
	}

	parts := []string{
		e.theme.TitleStyle.Render("Settings"), "", body, "", e.hint(),
	}
	if e.err != "" {
		parts = append(parts, e.theme.ErrorStyle.Render(e.err))
	}
	return e.theme.PickerStyle.Width(e.width).Render(
		lipgloss.JoinVertical(lipgloss.Left, parts...),
	)
}

func (e ConfigEditor) hint() string {
	switch e.phase {
	case editColor:
		return e.theme.DimStyle.Render(
			"←→ adjust · ↑↓ channel · tab light/dark · enter apply · esc back",
		)
	case editKeys:
		return e.theme.DimStyle.Render(
			"press keys to bind · ⌫ remove · enter apply · esc back",
		)
	case editText:
		return e.theme.DimStyle.Render("enter apply · esc back")
	default:
		return e.theme.DimStyle.Render(
			"↑↓ move · enter edit · changes apply on enter · esc close",
		)
	}
}

func (e ConfigEditor) listBody() string {
	var lines []string
	cursorLine := 0
	lastSection := ""
	for i, f := range e.fields {
		if f.section != lastSection {
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, e.theme.TitleStyle.Render(f.section))
			lastSection = f.section
		}
		if i == e.cursor {
			cursorLine = len(lines)
		}
		lines = append(lines, e.row(i, f))
	}
	return strings.Join(window(lines, cursorLine, e.height), "\n")
}

func (e ConfigEditor) row(i int, f cfgField) string {
	if e.phase == editText && i == e.cursor {
		return e.theme.FocusedLabelStyle.Render(
			"> "+f.label,
		) + "  " + e.input.View()
	}
	value := f.get(e.working)
	if f.kind == kindColor {
		value = colorSwatch(value) + " " + value
	}
	if i == e.cursor {
		return e.theme.SelRowTitleStyle.Render(
			component.Pad("> "+f.label, 18),
		) + "  " + value
	}
	return e.theme.LabelStyle.Render(
		component.Pad("  "+f.label, 18),
	) + "  " + e.theme.DimStyle.Render(
		value,
	)
}

// colorSwatch renders a small filled block in a colour value, so the field list
// previews each colour beside its text. A "light / dark" value swatches the light
// variant.
func colorSwatch(value string) string {
	c := strings.TrimSpace(value)
	if i := strings.Index(c, "/"); i >= 0 {
		c = strings.TrimSpace(c[:i])
	}
	return lipgloss.NewStyle().Background(lipgloss.Color(c)).Render("  ")
}

// window returns at most h lines centred on the line at focus, so a long field
// list scrolls to keep the cursor visible within the panel height.
func window(lines []string, focus, h int) []string {
	if h < 1 {
		h = 1
	}
	if len(lines) <= h {
		return lines
	}
	start := max(focus-h/2, 0)
	if start+h > len(lines) {
		start = len(lines) - h
	}
	return lines[start : start+h]
}

// colorEditor edits one theme colour with RGB sliders. A colour carries a light
// and a dark variant; the editor edits one variant at a time (tab toggles) with
// three channel sliders, and previews both variants live as swatches. It emits a
// "#hex" value (or "#light / #dark" when the variants differ) on commit, which the
// config editor parses back through parseColor.
type colorEditor struct {
	theme   theme.Theme
	light   [3]int
	dark    [3]int
	variant int
	channel int

	lastKey string
	repeat  int
}

func newColorEditor(theme theme.Theme, c config.Color) colorEditor {
	return colorEditor{
		theme: theme,
		light: parseRGB(c.Light),
		dark:  parseRGB(c.Dark),
	}
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
			return e.theme.FocusedLabelStyle.Render("● " + name)
		}
		return e.theme.DimStyle.Render("  " + name)
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
		label := e.theme.DimStyle.Render(name)
		value := e.theme.DimStyle.Render(component.Pad(strconv.Itoa(cur[i]), 3))
		if active {
			marker = e.theme.FocusedLabelStyle.Render("▸ ")
			label = e.theme.FocusedLabelStyle.Render(name)
			value = e.theme.FocusedLabelStyle.Render(
				component.Pad(strconv.Itoa(cur[i]), 3),
			)
		}
		rows = append(rows, fmt.Sprintf(
			"%s%s %s %s", marker, label, e.channelBar(cur[i], active), value,
		))
	}

	return lipgloss.JoinVertical(
		lipgloss.Left,
		e.theme.TitleStyle.Render(label),
		"",
		header,
		"",
		strings.Join(rows, "\n"),
	)
}

// channelBar renders a 0–255 channel value as a 24-cell filled bar. The active
// channel's fill uses the accent so the focused row reads at a glance; inactive
// rows are dimmed.
func (e colorEditor) channelBar(v int, active bool) string {
	const n = 24
	filled := v * n / 255
	fill := e.theme.DimStyle
	if active {
		fill = e.theme.MatchStyle
	}
	return fill.Render(strings.Repeat("█", filled)) +
		e.theme.DimStyle.Render(strings.Repeat("░", n-filled))
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

// recordEditor captures a key binding by listening for keypresses: each key the
// user presses is appended to the binding, like rebinding a control in a game.
// It knows the keys bound to every other action so it can warn the instant a
// captured key collides, before save validation would reject it. enter, esc and
// backspace are reserved by the editor as commit/cancel/remove, so they cannot be
// recorded here.
type recordEditor struct {
	theme  theme.Theme
	action string
	keys   []string
	other  map[string]string
	warn   string
}

func newRecordEditor(
	theme theme.Theme, action string, working config.Config,
) recordEditor {
	other := make(map[string]string)
	for a, ks := range working.Keys {
		if a == action {
			continue
		}
		for _, k := range ks {
			other[k] = a
		}
	}
	return recordEditor{theme: theme, action: action, other: other}
}

// add records a pressed key, ignoring an exact repeat of the last one, and warns
// when the key is already bound to another action.
func (e recordEditor) add(key string) recordEditor {
	if n := len(e.keys); n > 0 && e.keys[n-1] == key {
		return e
	}
	e.keys = append(e.keys, key)
	if a, ok := e.other[key]; ok {
		e.warn = fmt.Sprintf("%q is also bound to %q", key, a)
	} else {
		e.warn = ""
	}
	return e
}

func (e recordEditor) removeLast() recordEditor {
	if len(e.keys) > 0 {
		e.keys = e.keys[:len(e.keys)-1]
	}
	e.warn = ""
	return e
}

// value renders the recorded keys for commit as the comma-separated list the key
// field setter parses.
func (e recordEditor) value() string { return strings.Join(e.keys, ", ") }

func (e recordEditor) view() string {
	captured := e.theme.DimStyle.Render("(press a key)")
	if len(e.keys) > 0 {
		labels := make([]string, len(e.keys))
		for i, k := range e.keys {
			labels[i] = e.theme.MatchStyle.Render(component.KeyLabel(k))
		}
		captured = strings.Join(labels, " ")
	}

	lines := []string{
		e.theme.TitleStyle.Render("Bind " + e.action),
		"",
		captured,
	}
	if e.warn != "" {
		lines = append(lines, "", e.theme.ErrorStyle.Render(e.warn))
	}
	return strings.Join(lines, "\n")
}
