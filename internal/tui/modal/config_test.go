package modal

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/joakimcarlsson/wasa/internal/config"
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
func fieldIndex(e ConfigEditor, section, label string) int {
	for i, f := range e.fields {
		if f.section == section && f.label == label {
			return i
		}
	}
	return -1
}

func TestEditorRecordKeyCommitsOnApply(t *testing.T) {
	e := NewConfigEditor(testTheme(), config.Default(), 60, 20)
	e.cursor = fieldIndex(e, "Keys", config.ActionNew)
	if e.cursor < 0 {
		t.Fatal("new key field not found")
	}

	e, _ = e.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if e.phase != editKeys {
		t.Fatal("enter did not open the key recorder")
	}
	e, _ = e.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})

	e, cmd := e.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !emits[ConfigApplyMsg](cmd) {
		t.Fatal("commit did not emit ConfigApplyMsg")
	}
	if e.phase != editNone {
		t.Fatal("commit did not leave the recorder")
	}
	if got := e.Config().Keys[config.ActionNew]; len(got) != 1 ||
		got[0] != "x" {
		t.Fatalf("recorded binding not applied: %+v", got)
	}
}

func TestEditorColorSliderAdjustsAndCommits(t *testing.T) {
	e := NewConfigEditor(testTheme(), config.Default(), 60, 20)
	e.cursor = fieldIndex(e, "Theme", "accent")

	e, _ = e.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if e.phase != editColor {
		t.Fatal("enter did not open the colour picker")
	}
	before := e.color.value()
	e, _ = e.Update(tea.KeyMsg{Type: tea.KeyRight})
	if e.color.value() == before {
		t.Fatal("right arrow did not adjust the channel")
	}

	e, _ = e.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if e.phase != editNone {
		t.Fatal("commit did not leave the colour picker")
	}
	if got := e.Config().Theme.Accent.Light; !strings.HasPrefix(got, "#") {
		t.Fatalf("commit did not write a hex colour: %q", got)
	}
}

func TestEditorInvalidLayoutKeepsEditing(t *testing.T) {
	e := NewConfigEditor(testTheme(), config.Default(), 60, 20)
	e.cursor = fieldIndex(e, "Layout", "listColFrac")

	e, _ = e.Update(tea.KeyMsg{Type: tea.KeyEnter})
	e.input.SetValue("not-a-number")
	e, _ = e.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if e.phase != editText {
		t.Fatal("invalid commit should keep editing")
	}
	if e.err == "" {
		t.Fatal("invalid commit should surface an error")
	}
}

func TestRecordConflictWarns(t *testing.T) {
	r := newRecordEditor(testTheme(), config.ActionNew, config.Default())
	r = r.add("k")
	if r.warn == "" {
		t.Fatal("recording a bound key did not warn")
	}
}

func TestColorEditorAcceleratesOnHeldKey(t *testing.T) {
	e := newColorEditor(
		testTheme(),
		config.Color{Light: "#000000", Dark: "#000000"},
	)
	right := tea.KeyMsg{Type: tea.KeyRight}

	e = e.update(right)
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
	e := newColorEditor(
		testTheme(),
		config.Color{Light: "#808080", Dark: "#808080"},
	)
	for range 10 {
		e = e.update(tea.KeyMsg{Type: tea.KeyRight})
	}
	e = e.update(tea.KeyMsg{Type: tea.KeyLeft})
	if e.repeat != 0 {
		t.Fatalf("streak not reset on direction change: %d", e.repeat)
	}
}

func TestColorEditorHexRoundTrip(t *testing.T) {
	e := newColorEditor(
		testTheme(),
		config.Color{Light: "#7d56f4", Dark: "#7d56f4"},
	)
	if got := e.value(); got != "#7d56f4" {
		t.Fatalf("round-trip changed colour: %q", got)
	}
}

func TestEditorViewRendersFields(t *testing.T) {
	e := NewConfigEditor(testTheme(), config.Default(), 60, 100)
	out := e.View()
	for _, want := range []string{"Settings", "Theme", "Keys", "Layout", "accent"} {
		if !strings.Contains(out, want) {
			t.Errorf("panel view missing %q", want)
		}
	}
}
