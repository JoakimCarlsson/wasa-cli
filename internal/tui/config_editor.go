package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/joakimcarlsson/wasa/internal/config"
)

// cfgResult is what a configEditor update reports back to the cockpit.
type cfgResult int

const (
	cfgNone  cfgResult = iota
	cfgApply           // a field was committed; persist it and apply it live
	cfgClose           // leave the panel
)

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

// configEditor is the in-cockpit settings panel. It edits a working copy of the
// resolved config — theme colours, key bindings and layout — and on save hands
// that copy back to the cockpit, which persists it and applies it live. Colours
// are edited with RGB sliders, bindings by recording keypresses, layout values as
// numbers; every edit funnels back through the same string setters as config.json.
type configEditor struct {
	theme   Theme
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

func newConfigEditor(
	theme Theme, cfg config.Config, width, height int,
) configEditor {
	working := cfg
	working.Keys = make(config.Keys, len(cfg.Keys))
	for action, keys := range cfg.Keys {
		cp := make(config.KeyList, len(keys))
		copy(cp, keys)
		working.Keys[action] = cp
	}
	return configEditor{
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

func (e configEditor) update(msg tea.Msg) (configEditor, cfgResult, tea.Cmd) {
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
func (e configEditor) updateList(
	msg tea.Msg,
) (configEditor, cfgResult, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return e, cfgNone, nil
	}
	switch key.String() {
	case "esc", "q":
		return e, cfgClose, nil
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
	return e, cfgNone, nil
}

// beginEdit opens the sub-editor for the focused field, chosen by its kind.
func (e configEditor) beginEdit() (configEditor, cfgResult, tea.Cmd) {
	f := e.fields[e.cursor]
	e.err = ""
	switch f.kind {
	case kindColor:
		col, _ := parseColor(f.get(e.working))
		e.color = newColorEditor(e.theme, col)
		e.phase = editColor
		return e, cfgNone, nil
	case kindKeys:
		e.record = newRecordEditor(e.theme, f.label, e.working)
		e.phase = editKeys
		return e, cfgNone, nil
	default:
		e.input = textinput.New()
		e.input.CharLimit = 200
		e.input.SetValue(f.get(e.working))
		e.input.CursorEnd()
		e.input.Focus()
		e.phase = editText
		return e, cfgNone, textinput.Blink
	}
}

func (e configEditor) updateText(
	msg tea.Msg,
) (configEditor, cfgResult, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			return e.commit(e.input.Value())
		case "esc":
			e.phase = editNone
			e.err = ""
			return e, cfgNone, nil
		}
	}
	var cmd tea.Cmd
	e.input, cmd = e.input.Update(msg)
	return e, cfgNone, cmd
}

func (e configEditor) updateColor(
	msg tea.Msg,
) (configEditor, cfgResult, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return e, cfgNone, nil
	}
	switch key.String() {
	case "enter":
		return e.commit(e.color.value())
	case "esc":
		e.phase = editNone
		e.err = ""
		return e, cfgNone, nil
	}
	e.color = e.color.update(key)
	return e, cfgNone, nil
}

func (e configEditor) updateKeys(
	msg tea.Msg,
) (configEditor, cfgResult, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return e, cfgNone, nil
	}
	switch key.String() {
	case "enter":
		return e.commit(e.record.value())
	case "esc":
		e.phase = editNone
		e.err = ""
		return e, cfgNone, nil
	case "backspace", "ctrl+h":
		e.record = e.record.removeLast()
		return e, cfgNone, nil
	}
	e.record = e.record.add(key.String())
	return e, cfgNone, nil
}

// commit parses value into the focused field's setter. A parse error keeps the
// sub-editor open with the message; success returns to the field list.
func (e configEditor) commit(value string) (configEditor, cfgResult, tea.Cmd) {
	if err := e.fields[e.cursor].set(&e.working, value); err != nil {
		e.err = err.Error()
		return e, cfgNone, nil
	}
	e.phase = editNone
	e.err = ""
	return e, cfgApply, nil
}

// config returns the editor's working configuration, for the cockpit to persist
// and apply on save.
func (e configEditor) config() config.Config { return e.working }

func (e configEditor) view() string {
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

func (e configEditor) hint() string {
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

func (e configEditor) listBody() string {
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

func (e configEditor) row(i int, f cfgField) string {
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
			pad("> "+f.label, 18),
		) + "  " + value
	}
	return e.theme.LabelStyle.Render(
		pad("  "+f.label, 18),
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
