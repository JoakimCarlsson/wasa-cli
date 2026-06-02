package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/joakimcarlsson/wasa/internal/config"
	"github.com/joakimcarlsson/wasa/internal/registry"
)

// paneModel builds a one-workspace cockpit with a single running session
// selected, sized so the full (non-compact) frame renders.
func paneModel(t *testing.T) Model {
	t.Helper()
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	reg.AddSession(&registry.Session{
		ID: "s1", WorkspaceID: ws.ID, Branch: "feat/s1",
		TmuxName: "wasa_x_s1", Status: registry.StatusRunning,
	})
	m := New(t.TempDir(), reg, ws.ID, config.Default())
	m.width, m.height = 120, 30
	return m
}

func TestCyclePaneTabWraps(t *testing.T) {
	m := paneModel(t)
	if m.pane != panePreview {
		t.Fatalf("initial pane = %d, want panePreview", m.pane)
	}
	m.cyclePaneTab(1)
	if m.pane != paneDiff {
		t.Fatalf("after one cycle pane = %d, want paneDiff", m.pane)
	}
	m.cyclePaneTab(1)
	if m.pane != paneTerminal {
		t.Fatalf("after two cycles pane = %d, want paneTerminal", m.pane)
	}
	m.cyclePaneTab(1)
	if m.pane != panePreview {
		t.Fatalf("cycle past end pane = %d, want wrap to panePreview", m.pane)
	}
}

// TestPaneTabGatesPreviewTarget is the guard that only the active tab does
// per-tick work: with a running session selected the Preview tab targets its
// tmux stream, but cycling to Diff or Terminal must yield no target, so the
// watcher tears down and neither the stream nor the capture poll runs.
func TestPaneTabGatesPreviewTarget(t *testing.T) {
	m := paneModel(t)
	if got := m.previewTarget(); got != "wasa_x_s1" {
		t.Fatalf("Preview tab target = %q, want the session's tmux name", got)
	}
	m.cyclePaneTab(1) // Diff
	if got := m.previewTarget(); got != "" {
		t.Fatalf("Diff tab target = %q, want empty", got)
	}
	m.cyclePaneTab(1) // Terminal
	if got := m.previewTarget(); got != "" {
		t.Fatalf("Terminal tab target = %q, want empty", got)
	}
	m.cyclePaneTab(1) // back to Preview
	if got := m.previewTarget(); got != "wasa_x_s1" {
		t.Fatalf("returning to Preview target = %q, want resumed", got)
	}
}

func TestPaneTabKeyCyclesPane(t *testing.T) {
	m := paneModel(t)
	ctrlT := m.keys.Primary(config.ActionPaneTab)
	if ctrlT == "" {
		t.Fatal("pane-tab action is unbound")
	}

	next, _ := m.updateList(tea.KeyMsg{Type: tea.KeyCtrlT})
	got := next.(Model)
	if got.pane != paneDiff {
		t.Fatalf("pane-tab key did not advance the pane: %d", got.pane)
	}
}

func TestPaneTabStripRendersAllTabs(t *testing.T) {
	m := paneModel(t)
	view := m.View()
	for _, label := range paneTabNames {
		if !strings.Contains(view, label) {
			t.Fatalf("view missing pane tab %q:\n%s", label, view)
		}
	}
}
