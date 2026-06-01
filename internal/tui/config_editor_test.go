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
	if c, err := parseColor("#abc"); err != nil || c != (config.Color{Light: "#abc", Dark: "#abc"}) {
		t.Fatalf("single form: %+v %v", c, err)
	}
	if c, err := parseColor("#aaa / #bbb"); err != nil || c != (config.Color{Light: "#aaa", Dark: "#bbb"}) {
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

func TestEditorEditAndSaveFlow(t *testing.T) {
	e := newConfigEditor(config.Default(), 60, 20)
	e.cursor = fieldIndex(e, "Keys", config.ActionNew)
	if e.cursor < 0 {
		t.Fatal("new key field not found")
	}

	e, _, _ = e.update(tea.KeyMsg{Type: tea.KeyEnter}) // start editing
	if !e.editing {
		t.Fatal("enter did not start editing")
	}
	e.input.SetValue("x")
	e, _, _ = e.update(tea.KeyMsg{Type: tea.KeyEnter}) // commit
	if e.editing {
		t.Fatal("commit did not leave edit mode")
	}

	_, result, _ := e.update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if result != cfgSave {
		t.Fatalf("ctrl+s result = %v, want save", result)
	}
	if got := e.config().Keys[config.ActionNew]; len(got) != 1 || got[0] != "x" {
		t.Fatalf("working config not updated: %+v", got)
	}
}

func TestEditorInvalidCommitShowsErrorAndKeepsEditing(t *testing.T) {
	e := newConfigEditor(config.Default(), 60, 20)
	e.cursor = fieldIndex(e, "Layout", "listColFrac")

	e, _, _ = e.update(tea.KeyMsg{Type: tea.KeyEnter})
	e.input.SetValue("not-a-number")
	e, _, _ = e.update(tea.KeyMsg{Type: tea.KeyEnter})

	if !e.editing {
		t.Fatal("invalid commit should keep editing")
	}
	if e.err == "" {
		t.Fatal("invalid commit should surface an error")
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

func TestSaveConfigPersistsAndAppliesLive(t *testing.T) {
	defer applyTheme(config.Default().Theme)

	home := t.TempDir()
	m := editorModel(t, home)

	cfg := config.Default()
	cfg.Theme.Accent = config.Color{Light: "#abcdef", Dark: "#abcdef"}
	cfg.Keys[config.ActionNew] = config.KeyList{"x"}

	m.mode = modeConfig
	next, _ := m.saveConfig(cfg)
	got := next.(Model)

	if got.mode != modeList {
		t.Fatal("save did not return to the list")
	}
	if got.keys.action("x") != config.ActionNew {
		t.Fatal("save did not apply the new binding live")
	}
	if titleStyle.GetForeground() != lipgloss.Color("#abcdef") {
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
	e := newConfigEditor(config.Default(), 60, 100)
	out := e.view()
	for _, want := range []string{"Settings", "Theme", "Keys", "Layout", "accent"} {
		if !strings.Contains(out, want) {
			t.Errorf("panel view missing %q", want)
		}
	}
}

func TestSaveConfigRejectsInvalidWithoutClobbering(t *testing.T) {
	home := t.TempDir()
	m := editorModel(t, home)

	cfg := config.Default()
	cfg.Keys[config.ActionNew] = config.KeyList{"k"} // dup with kill

	m.mode = modeConfig
	next, _ := m.saveConfig(cfg)
	got := next.(Model)

	if got.mode != modeConfig {
		t.Fatal("invalid save should keep the panel open")
	}
	if _, err := config.Load(home); err != nil {
		t.Fatalf("invalid save wrote a bad file: %v", err)
	}
}
