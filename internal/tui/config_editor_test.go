package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/joakimcarlsson/wasa/internal/config"
	"github.com/joakimcarlsson/wasa/internal/registry"
)

func TestParseColorForms(t *testing.T) {
	if c, err := parseColor(
		"#abc",
	); err != nil ||
		c != (config.Color{Light: "#abc", Dark: "#abc"}) {
		t.Fatalf("single form: %+v %v", c, err)
	}
	if c, err := parseColor(
		"#aaa / #bbb",
	); err != nil ||
		c != (config.Color{Light: "#aaa", Dark: "#bbb"}) {
		t.Fatalf("split form: %+v %v", c, err)
	}
	if _, err := parseColor("  "); err == nil {
		t.Fatal("empty colour should error")
	}
}

func TestParseKeysSplitsAndRejectsEmpty(t *testing.T) {
	if k, err := parseKeys("x, ctrl+n ,"); err != nil ||
		len(k) != 2 || k[0] != "x" || k[1] != "ctrl+n" {
		t.Fatalf("parseKeys: %+v %v", k, err)
	}
	if _, err := parseKeys("  ,  "); err == nil {
		t.Fatal("empty binding should error")
	}
}

// fieldIndex returns the editor field index for a section/label pair.
func fieldIndex(e configEditor, section, label string) int {
	for i, f := range e.fields {
		if f.section == section && f.label == label {
			return i
		}
	}
	return -1
}

func TestEditorRecordKeyCommitsOnApply(t *testing.T) {
	e := newConfigEditor(testTheme(), config.Default(), 60, 20)
	e.cursor = fieldIndex(e, "Keys", config.ActionNew)
	if e.cursor < 0 {
		t.Fatal("new key field not found")
	}

	e, _, _ = e.update(tea.KeyMsg{Type: tea.KeyEnter}) // start recording
	if e.phase != editKeys {
		t.Fatal("enter did not open the key recorder")
	}
	e, _, _ = e.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})

	e, result, _ := e.update(tea.KeyMsg{Type: tea.KeyEnter}) // commit
	if result != cfgApply {
		t.Fatalf("commit result = %v, want apply", result)
	}
	if e.phase != editNone {
		t.Fatal("commit did not leave the recorder")
	}
	if got := e.config().Keys[config.ActionNew]; len(got) != 1 ||
		got[0] != "x" {
		t.Fatalf("recorded binding not applied: %+v", got)
	}
}

func TestEditorColorSliderAdjustsAndCommits(t *testing.T) {
	e := newConfigEditor(testTheme(), config.Default(), 60, 20)
	e.cursor = fieldIndex(e, "Theme", "accent")

	e, _, _ = e.update(tea.KeyMsg{Type: tea.KeyEnter}) // open sliders
	if e.phase != editColor {
		t.Fatal("enter did not open the colour picker")
	}
	before := e.color.value()
	e, _, _ = e.update(tea.KeyMsg{Type: tea.KeyRight}) // +1 on R
	if e.color.value() == before {
		t.Fatal("right arrow did not adjust the channel")
	}

	e, _, _ = e.update(tea.KeyMsg{Type: tea.KeyEnter}) // commit
	if e.phase != editNone {
		t.Fatal("commit did not leave the colour picker")
	}
	if got := e.config().Theme.Accent.Light; !strings.HasPrefix(got, "#") {
		t.Fatalf("commit did not write a hex colour: %q", got)
	}
}

func TestEditorInvalidLayoutKeepsEditing(t *testing.T) {
	e := newConfigEditor(testTheme(), config.Default(), 60, 20)
	e.cursor = fieldIndex(e, "Layout", "listColFrac")

	e, _, _ = e.update(tea.KeyMsg{Type: tea.KeyEnter})
	e.input.SetValue("not-a-number")
	e, _, _ = e.update(tea.KeyMsg{Type: tea.KeyEnter})

	if e.phase != editText {
		t.Fatal("invalid commit should keep editing")
	}
	if e.err == "" {
		t.Fatal("invalid commit should surface an error")
	}
}

func TestRecordConflictWarns(t *testing.T) {
	r := newRecordEditor(testTheme(), config.ActionNew, config.Default())
	r = r.add("k") // already bound to kill
	if r.warn == "" {
		t.Fatal("recording a bound key did not warn")
	}
}

func TestColorEditorAcceleratesOnHeldKey(t *testing.T) {
	e := newColorEditor(testTheme(), config.Color{Light: "#000000", Dark: "#000000"})
	right := tea.KeyMsg{Type: tea.KeyRight}

	e = e.update(right) // first press: +1
	if e.light[0] != 1 {
		t.Fatalf("first press = %d, want 1", e.light[0])
	}
	prev := e.light[0]
	for range 12 {
		e = e.update(right)
	}
	gained := e.light[0] - prev
	if gained <= 12 {
		t.Fatalf(
			"held key did not accelerate: gained %d over 12 presses",
			gained,
		)
	}
}

func TestColorEditorResetsAccelOnDirectionChange(t *testing.T) {
	e := newColorEditor(testTheme(), config.Color{Light: "#808080", Dark: "#808080"})
	for range 10 {
		e = e.update(tea.KeyMsg{Type: tea.KeyRight})
	}
	e = e.update(tea.KeyMsg{Type: tea.KeyLeft}) // different key resets streak
	if e.repeat != 0 {
		t.Fatalf("streak not reset on direction change: %d", e.repeat)
	}
}

func TestColorEditorHexRoundTrip(t *testing.T) {
	e := newColorEditor(testTheme(), config.Color{Light: "#7d56f4", Dark: "#7d56f4"})
	if got := e.value(); got != "#7d56f4" {
		t.Fatalf("round-trip changed colour: %q", got)
	}
}

// editorModel builds a one-workspace cockpit rooted at home, so saveConfig writes
// into a known $WASA_HOME.
func editorModel(t *testing.T, home string) Model {
	t.Helper()
	reg, err := registry.Open(home)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	return New(home, reg, ws.ID, config.Default())
}

func TestConfigActionOpensPanel(t *testing.T) {
	m := editorModel(t, t.TempDir())
	next, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(",")})
	if next.(Model).mode != modeConfig {
		t.Fatal("config action did not open the settings panel")
	}
}

func TestApplyConfigPersistsAndAppliesLive(t *testing.T) {
	home := t.TempDir()
	m := editorModel(t, home)

	cfg := config.Default()
	cfg.Theme.Accent = config.Color{Light: "#abcdef", Dark: "#abcdef"}
	cfg.Keys[config.ActionNew] = config.KeyList{"x"}

	m.mode = modeConfig
	next, _ := m.applyConfig(cfg)
	got := next.(Model)

	if got.mode != modeConfig {
		t.Fatal("apply should keep the panel open")
	}
	if got.keys.action("x") != config.ActionNew {
		t.Fatal("apply did not apply the new binding live")
	}
	if got.theme.TitleStyle.GetForeground() != lipgloss.Color("#abcdef") {
		t.Fatal("save did not recolour live")
	}

	reloaded, err := config.Load(home)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Theme.Accent.Light != "#abcdef" {
		t.Fatalf("config not persisted: %+v", reloaded.Theme.Accent)
	}
	if len(reloaded.Keys[config.ActionNew]) != 1 ||
		reloaded.Keys[config.ActionNew][0] != "x" {
		t.Fatalf("binding not persisted: %+v", reloaded.Keys[config.ActionNew])
	}
}

func TestEditorViewRendersFields(t *testing.T) {
	e := newConfigEditor(testTheme(), config.Default(), 60, 100)
	out := e.view()
	for _, want := range []string{"Settings", "Theme", "Keys", "Layout", "accent"} {
		if !strings.Contains(out, want) {
			t.Errorf("panel view missing %q", want)
		}
	}
}

func TestApplyConfigRejectsInvalidWithoutClobbering(t *testing.T) {
	home := t.TempDir()
	m := editorModel(t, home)

	cfg := config.Default()
	cfg.Keys[config.ActionNew] = config.KeyList{"k"} // dup with kill

	m.mode = modeConfig
	next, _ := m.applyConfig(cfg)
	got := next.(Model)

	if got.mode != modeConfig {
		t.Fatal("invalid apply should keep the panel open")
	}
	if got.editor.err == "" {
		t.Fatal("invalid apply should surface an error on the panel")
	}
	if _, err := config.Load(home); err != nil {
		t.Fatalf("invalid apply wrote a bad file: %v", err)
	}
}
