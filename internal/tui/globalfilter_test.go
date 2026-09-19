package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/joakimcarlsson/wasa-cli/internal/config"
	"github.com/joakimcarlsson/wasa-cli/internal/registry"
)

// jumpModel builds a model over two workspaces whose sessions overlap in name,
// so the global jump can be exercised across workspace boundaries and against
// same-named sessions in different repos. The model opens on workspace "alpha".
func jumpModel(t *testing.T) Model {
	t.Helper()
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	alpha, _ := reg.EnsureWorkspace("/repo/alpha", "", "alpha")
	beta, _ := reg.EnsureWorkspace("/repo/beta", "", "beta")

	sessions := []struct {
		id, wsID, title, branch string
		running                 bool
	}{
		{"a1", alpha.ID, "shared work", "feat/shared", true},
		{"a2", alpha.ID, "alpha only", "feat/alpha", true},
		{"b1", beta.ID, "shared work", "feat/shared", false},
		{"b2", beta.ID, "beta payments", "feat/payments", true},
	}
	for _, s := range sessions {
		status := registry.StatusExited
		if s.running {
			status = registry.StatusRunning
		}
		reg.AddSession(&registry.Session{
			ID: s.id, WorkspaceID: s.wsID, Title: s.title,
			Branch: s.branch, Status: status, TmuxName: "t-" + s.id,
		})
	}

	m := New(t.TempDir(), reg, alpha.ID, config.Default())
	m.width, m.height = 120, 30
	return m
}

// openJump presses the global-jump binding and returns the resulting model.
func openJump(t *testing.T, m Model) Model {
	t.Helper()
	next, _ := m.updateList(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	return next.(Model)
}

// typeJump feeds a fragment into the open jump input one keystroke at a time,
// the way real input arrives, and returns the resulting model.
func typeJump(t *testing.T, m Model, text string) Model {
	t.Helper()
	for _, r := range text {
		next, _ := m.updateGlobalFilter(
			tea.KeyPressMsg{Text: string(r), Code: r},
		)
		m = next.(Model)
	}
	return m
}

func matchIDs(ms []globalMatch) []string {
	ids := make([]string, len(ms))
	for i, mt := range ms {
		ids[i] = mt.session.ID
	}
	return ids
}

func TestGlobalFilterKeyOpensJump(t *testing.T) {
	m := openJump(t, jumpModel(t))
	if !m.globalFilter.active || m.mode != modeGlobalFilter {
		t.Fatal("ctrl+g did not open the global jump")
	}
	if m.filter.active {
		t.Fatal("global jump activated the per-workspace filter")
	}
	if got := len(m.globalFilter.matches); got != 4 {
		t.Fatalf("global jump opened with %d candidates, want all 4", got)
	}
}

func TestGlobalFilterIsNoopWithoutSessions(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	m := New(t.TempDir(), reg, ws.ID, config.Default())

	next, _ := m.updateList(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if next.(Model).globalFilter.active {
		t.Fatal("global jump opened over an empty registry")
	}
}

// TestGlobalFilterMatchesOtherWorkspace types a fragment that exists only in
// workspace beta while workspace alpha is active.
func TestGlobalFilterMatchesOtherWorkspace(t *testing.T) {
	m := typeJump(t, openJump(t, jumpModel(t)), "payments")
	got := matchIDs(m.globalFilter.matches)
	if len(got) != 1 || got[0] != "b2" {
		t.Fatalf("jump \"payments\" = %v, want [b2]", got)
	}
	if name := m.globalFilter.matches[0].wsName; name != "beta" {
		t.Fatalf("match qualifier = %q, want \"beta\"", name)
	}
}

// TestGlobalFilterSpansWorkspaces types a fragment shared by a session in each
// workspace, which the per-workspace filter could never surface together.
func TestGlobalFilterSpansWorkspaces(t *testing.T) {
	m := typeJump(t, openJump(t, jumpModel(t)), "shared")
	got := matchIDs(m.globalFilter.matches)
	want := map[string]bool{"a1": true, "b1": true}
	if len(got) != len(want) {
		t.Fatalf("jump \"shared\" = %v, want one match per workspace", got)
	}
	for _, id := range got {
		if !want[id] {
			t.Fatalf("jump \"shared\" returned unexpected session %q", id)
		}
	}
}

func TestGlobalFilterStatusToken(t *testing.T) {
	m := typeJump(t, openJump(t, jumpModel(t)), "exited shared")
	got := matchIDs(m.globalFilter.matches)
	if len(got) != 1 || got[0] != "b1" {
		t.Fatalf("jump \"exited shared\" = %v, want [b1]", got)
	}
}

// TestGlobalFilterJumpSwitchesWorkspace accepts a match in workspace beta from
// workspace alpha and checks the active tab and cursor both follow it.
func TestGlobalFilterJumpSwitchesWorkspace(t *testing.T) {
	m := jumpModel(t)
	startID := m.activeID
	m = typeJump(t, openJump(t, m), "payments")

	next, _ := m.updateGlobalFilter(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)

	if m.activeID == startID {
		t.Fatal("jump did not switch the active workspace")
	}
	if m.mode != modeList || m.globalFilter.active {
		t.Fatal("jump left the overlay open")
	}
	if m.filter.active {
		t.Fatal("jump left the per-workspace filter active")
	}
	sel := m.selectedSession()
	if sel == nil || sel.ID != "b2" {
		t.Fatalf("cursor landed on %v, want session b2", sel)
	}
}

func TestGlobalFilterEscapeChangesNothing(t *testing.T) {
	m := jumpModel(t)
	m.cursor = 1
	startID, startCursor := m.activeID, m.cursor
	m = typeJump(t, openJump(t, m), "payments")

	next, _ := m.updateGlobalFilter(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)

	if m.globalFilter.active || m.mode != modeList {
		t.Fatal("escape did not close the global jump")
	}
	if m.activeID != startID || m.cursor != startCursor {
		t.Fatalf(
			"escape moved to workspace %q cursor %d, want %q/%d",
			m.activeID, m.cursor, startID, startCursor,
		)
	}
}

// TestPerWorkspaceFilterStaysScoped checks the existing ctrl+f filter is
// unaffected by the global jump: it still sees only the active workspace.
func TestPerWorkspaceFilterStaysScoped(t *testing.T) {
	m := jumpModel(t)
	next, _ := m.updateList(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	m = typeFilter(t, next.(Model), "shared")

	got := sessionIDs(m.sessions())
	if len(got) != 1 || got[0] != "a1" {
		t.Fatalf("ctrl+f \"shared\" = %v, want only alpha's [a1]", got)
	}
	if m.globalFilter.active {
		t.Fatal("ctrl+f opened the global jump")
	}
}
