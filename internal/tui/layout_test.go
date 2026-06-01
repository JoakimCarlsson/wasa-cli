package tui

import (
	"testing"

	"github.com/joakimcarlsson/wasa/internal/config"
	"github.com/joakimcarlsson/wasa/internal/registry"
)

func layoutModel(t *testing.T, cfg config.Config, width int) Model {
	t.Helper()
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	m := New(t.TempDir(), reg, ws.ID, cfg)
	m.width = width
	return m
}

func TestListColWidthUsesFraction(t *testing.T) {
	m := layoutModel(t, config.Default(), 200)
	if got, want := m.listColWidth(), int(200*0.34); got != want {
		t.Fatalf("default listColWidth = %d, want %d", got, want)
	}
}

func TestListColWidthOverrideWidensColumn(t *testing.T) {
	cfg := config.Default()
	cfg.Layout.ListColFrac = 0.6
	m := layoutModel(t, cfg, 200)

	def := layoutModel(t, config.Default(), 200)
	if m.listColWidth() <= def.listColWidth() {
		t.Fatalf(
			"override frac did not widen column: %d vs default %d",
			m.listColWidth(), def.listColWidth(),
		)
	}
	if got, want := m.listColWidth(), int(200*0.6); got != want {
		t.Fatalf("override listColWidth = %d, want %d", got, want)
	}
}

func TestListColWidthFlooredAtMinimum(t *testing.T) {
	cfg := config.Default()
	cfg.Layout.MinListWidth = 50
	m := layoutModel(t, cfg, 100)

	if got := m.listColWidth(); got != 50 {
		t.Fatalf("listColWidth = %d, want floor 50", got)
	}
}
