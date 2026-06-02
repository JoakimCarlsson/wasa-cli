package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/joakimcarlsson/wasa/internal/config"
	"github.com/joakimcarlsson/wasa/internal/registry"
)

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
	if got.keys.Action("x") != config.ActionNew {
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
	if got.editor.Err() == "" {
		t.Fatal("invalid apply should surface an error on the panel")
	}
	if _, err := config.Load(home); err != nil {
		t.Fatalf("invalid apply wrote a bad file: %v", err)
	}
}
