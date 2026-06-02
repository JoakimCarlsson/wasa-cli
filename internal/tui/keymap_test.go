package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/joakimcarlsson/wasa/internal/config"
	"github.com/joakimcarlsson/wasa/internal/registry"
)

// remapModel builds a one-workspace cockpit whose config remaps "new" to the
// given key, for asserting the cockpit acts on the resolved binding.
func remapModel(t *testing.T, key string) Model {
	t.Helper()
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")

	cfg := config.Default()
	cfg.Keys[config.ActionNew] = config.KeyList{key}
	return New(t.TempDir(), reg, ws.ID, cfg)
}

func TestRemappedKeyTriggersAction(t *testing.T) {
	m := remapModel(t, "x")

	next, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if next.(Model).mode != modeCreate {
		t.Fatal("remapped key x did not open the create form")
	}
}

func TestOldKeyInertAfterRemap(t *testing.T) {
	m := remapModel(t, "x")

	next, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if next.(Model).mode != modeList {
		t.Fatal("old key n still opened the create form after remap")
	}
}

func TestMenuShowsRemappedKey(t *testing.T) {
	m := remapModel(t, "x")

	menu := m.menuBar()
	if !strings.Contains(menu, "x") {
		t.Errorf("menu does not show remapped new key:\n%s", menu)
	}
}
