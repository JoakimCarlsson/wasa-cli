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
	cfgNone cfgResult = iota
	cfgSave
	cfgCancel
)

// cfgField is one editable setting in the config panel: a labelled value under a
// section, with a getter that renders the working value as text and a setter that
// parses typed text back into the working config (returning a parse error the
// panel shows inline).
type cfgField struct {
	section string
	label   string
	get     func(c config.Config) string
	set     func(c *config.Config, s string) error
}

// configEditor is the in-cockpit settings panel. It edits a working copy of the
// resolved config — theme colours, key bindings and layout — and on save hands
// that copy back to the cockpit, which persists it and applies it live. Editing
// is text-based: a colour is "#hex" or "light / dark", a binding is a
// comma-separated key list, a layout value is a number, matching config.json.
type configEditor struct {
	working config.Config
	fields  []cfgField
	cursor  int
	editing bool
	input   textinput.Model
	err     string
	width   int
	height  int
}

func newConfigEditor(cfg config.Config, width, height int) configEditor {
	working := cfg
	working.Keys = make(config.Keys, len(cfg.Keys))
	for action, keys := range cfg.Keys {
		cp := make(config.KeyList, len(keys))
		copy(cp, keys)
		working.Keys[action] = cp
	}
	return configEditor{
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
		{"selectionFg", func(t *config.Theme) *config.Color { return &t.SelectionFg }},
		{"selectionBg", func(t *config.Theme) *config.Color { return &t.SelectionBg }},
		{"danger", func(t *config.Theme) *config.Color { return &t.Danger }},
		{"onAccent", func(t *config.Theme) *config.Color { return &t.OnAccent }},
		{"inactiveBtnBg", func(t *config.Theme) *config.Color { return &t.InactiveBtnBg }},
		{"menuKey", func(t *config.Theme) *config.Color { return &t.MenuKey }},
		{"menuDesc", func(t *config.Theme) *config.Color { return &t.MenuDesc }},
		{"menuSep", func(t *config.Theme) *config.Color { return &t.MenuSep }},
	}
	for _, c := range colors {
		ptr := c.ptr
		fs = append(fs, cfgField{
			section: "Theme",
			label:   c.label,
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
	return fs
}

func floatField(label string, ptr func(*config.Layout) *float64) cfgField {
	return cfgField{
		section: "Layout",
		label:   label,
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

// showColor renders a colour for editing: a single value when the light and dark
// variants match, "light / dark" when they differ.
func showColor(c config.Color) string {
	if c.Light == c.Dark {
		return c.Light
	}
	return c.Light + " / " + c.Dark
}

// parseColor parses an edited colour value. "#hex" (or an ANSI index) sets both
// variants; "light / dark" sets them independently.
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
			return config.Color{}, fmt.Errorf(
				`use "#hex" or "light / dark"`,
			)
		}
		return config.Color{Light: l, Dark: d}, nil
	default:
		return config.Color{}, fmt.Errorf(`use "#hex" or "light / dark"`)
	}
}

// parseKeys parses an edited binding: a comma-separated list of key strings.
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
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		if e.editing {
			var cmd tea.Cmd
			e.input, cmd = e.input.Update(msg)
			return e, cfgNone, cmd
		}
		return e, cfgNone, nil
	}

	if e.editing {
		switch key.String() {
		case "enter":
			if err := e.fields[e.cursor].set(
				&e.working, e.input.Value(),
			); err != nil {
				e.err = err.Error()
				return e, cfgNone, nil
			}
			e.editing = false
			e.err = ""
			return e, cfgNone, nil
		case "esc":
			e.editing = false
			e.err = ""
			return e, cfgNone, nil
		}
		var cmd tea.Cmd
		e.input, cmd = e.input.Update(msg)
		return e, cfgNone, cmd
	}

	switch key.String() {
	case "esc", "q":
		return e, cfgCancel, nil
	case "ctrl+s":
		return e, cfgSave, nil
	case "up", "k":
		if e.cursor > 0 {
			e.cursor--
		}
	case "down", "j":
		if e.cursor < len(e.fields)-1 {
			e.cursor++
		}
	case "enter":
		e.input = textinput.New()
		e.input.CharLimit = 200
		e.input.SetValue(e.fields[e.cursor].get(e.working))
		e.input.CursorEnd()
		e.input.Focus()
		e.editing = true
		e.err = ""
		return e, cfgNone, textinput.Blink
	}
	return e, cfgNone, nil
}

// config returns the editor's working configuration, for the cockpit to persist
// and apply on save.
func (e configEditor) config() config.Config { return e.working }

func (e configEditor) view() string {
	var lines []string
	cursorLine := 0
	lastSection := ""
	for i, f := range e.fields {
		if f.section != lastSection {
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, titleStyle.Render(f.section))
			lastSection = f.section
		}
		if i == e.cursor {
			cursorLine = len(lines)
		}
		lines = append(lines, e.row(i, f))
	}

	body := strings.Join(window(lines, cursorLine, e.height), "\n")

	hint := dimStyle.Render(
		"↑↓ move · enter edit · ctrl+s save · esc cancel",
	)
	if e.editing {
		hint = dimStyle.Render("enter commit · esc discard field")
	}
	parts := []string{titleStyle.Render("Settings"), "", body, "", hint}
	if e.err != "" {
		parts = append(parts, errorStyle.Render(e.err))
	}
	return pickerStyle.Width(e.width).Render(
		lipgloss.JoinVertical(lipgloss.Left, parts...),
	)
}

func (e configEditor) row(i int, f cfgField) string {
	label := pad("  "+f.label, 18)
	if e.editing && i == e.cursor {
		return focusedLabelStyle.Render("> "+f.label) + "  " + e.input.View()
	}
	value := f.get(e.working)
	if i == e.cursor {
		return selRowTitleStyle.Render(pad("> "+f.label, 18) + "  " + value)
	}
	return label + "  " + dimStyle.Render(value)
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
