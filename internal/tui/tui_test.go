package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/joakimcarlsson/wasa/internal/registry"
)

// testModel builds a model over two workspaces, wsA and wsB, with wsA more
// recently used so it sorts first. It returns the model and the two workspace
// ids.
func testModel(t *testing.T) (Model, string, string) {
	t.Helper()
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	wsA, _ := reg.EnsureWorkspace("/repo-a", "", "repo-a")
	wsB, _ := reg.EnsureWorkspace("/repo-b", "", "repo-b")

	reg.AddSession(&registry.Session{
		ID: "a1", WorkspaceID: wsA.ID, Branch: "feat/a1",
	})
	reg.AddSession(&registry.Session{
		ID: "a2", WorkspaceID: wsA.ID, Branch: "feat/a2",
	})
	reg.AddSession(&registry.Session{
		ID: "b1", WorkspaceID: wsB.ID, Branch: "feat/b1",
	})

	wsA.LastUsedAt = time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	wsB.LastUsedAt = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	return New(t.TempDir(), reg, wsA.ID), wsA.ID, wsB.ID
}

func TestNewActivatesCurrentWorkspace(t *testing.T) {
	m, _, wsB := testModel(t)

	reg := m.reg
	m2 := New(t.TempDir(), reg, wsB)
	if m2.activeID != wsB {
		t.Fatalf("activeID = %q, want current workspace %q", m2.activeID, wsB)
	}

	m3 := New(t.TempDir(), reg, "")
	if m3.activeID != m.workspaces[0].ID {
		t.Fatalf(
			"activeID = %q, want MRU first %q",
			m3.activeID,
			m.workspaces[0].ID,
		)
	}
}

func TestSessionsFilteredByActiveTab(t *testing.T) {
	m, wsA, wsB := testModel(t)

	if got := len(m.sessions()); got != 2 {
		t.Fatalf("wsA sessions = %d, want 2", got)
	}
	if m.activeID != wsA {
		t.Fatalf("activeID = %q, want %q", m.activeID, wsA)
	}

	m.cycleTab(1)
	if m.activeID != wsB {
		t.Fatalf("after cycle activeID = %q, want %q", m.activeID, wsB)
	}
	if got := len(m.sessions()); got != 1 {
		t.Fatalf("wsB sessions = %d, want 1", got)
	}
}

func TestCycleTabWraps(t *testing.T) {
	m, wsA, wsB := testModel(t)

	m.cursor = 1
	m.cycleTab(1)
	if m.activeID != wsB {
		t.Fatalf("activeID = %q, want %q", m.activeID, wsB)
	}
	if m.cursor != 0 {
		t.Fatalf("cursor = %d, want reset to 0 on tab change", m.cursor)
	}

	m.cycleTab(1)
	if m.activeID != wsA {
		t.Fatalf(
			"cycle past end activeID = %q, want wrap to %q",
			m.activeID,
			wsA,
		)
	}
}

func TestRefreshFollowsActiveWorkspaceById(t *testing.T) {
	m, _, wsB := testModel(t)

	m.cycleTab(1)
	if m.activeID != wsB {
		t.Fatalf("precondition: activeID = %q, want %q", m.activeID, wsB)
	}

	wsBPtr, _ := m.reg.Workspace(wsB)
	wsBPtr.LastUsedAt = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	m.refresh()
	if m.activeID != wsB {
		t.Fatalf(
			"active tab moved on reorder: activeID = %q, want %q",
			m.activeID,
			wsB,
		)
	}
}

func TestRefreshClampsCursor(t *testing.T) {
	m, _, _ := testModel(t)
	m.cursor = 5
	m.refresh()
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want clamped to last index 1", m.cursor)
	}
}

func TestEnterCreatePreselectsDefaultProfile(t *testing.T) {
	m, _, _ := testModel(t)

	next, _ := m.enterCreate()
	got := next.(Model)
	if got.mode != modeCreate {
		t.Fatal("enterCreate did not switch to create mode")
	}
	if len(got.form.profiles) == 0 {
		t.Fatal("create form has no profiles")
	}
	if got.form.profIdx != 0 {
		t.Fatalf(
			"profIdx = %d, want default profile preselected (0)",
			got.form.profIdx,
		)
	}
}

func TestEnterCreateWithNoWorkspaceOpensPlainForm(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	m := New(t.TempDir(), reg, "")
	if m.currentWorkspace() != nil {
		t.Fatal("precondition: expected no current workspace")
	}

	next, _ := m.enterCreate()
	got := next.(Model)
	if got.mode != modeCreate {
		t.Fatal("enterCreate did not open the form when there is no workspace")
	}
	if len(got.form.profiles) != 0 {
		t.Fatalf(
			"form profiles = %v, want none without a workspace",
			got.form.profiles,
		)
	}

	params := got.form.params()
	if params.Branch != "" {
		t.Fatalf(
			"default params carried a branch %q, want a plain session",
			params.Branch,
		)
	}
	if params.WorkingDir == "" {
		t.Fatal(
			"plain session has no working directory; want the current directory",
		)
	}
}

func TestListCursorNavigation(t *testing.T) {
	m, _, _ := testModel(t)

	down := tea.KeyMsg{Type: tea.KeyDown}
	next, _ := m.updateList(down)
	m = next.(Model)
	if m.cursor != 1 {
		t.Fatalf("cursor after down = %d, want 1", m.cursor)
	}

	next, _ = m.updateList(down)
	m = next.(Model)
	if m.cursor != 1 {
		t.Fatalf("cursor clamped at last = %d, want 1", m.cursor)
	}

	up := tea.KeyMsg{Type: tea.KeyUp}
	next, _ = m.updateList(up)
	m = next.(Model)
	if m.cursor != 0 {
		t.Fatalf("cursor after up = %d, want 0", m.cursor)
	}
}

func TestEnterConfirmDeleteCapturesSelection(t *testing.T) {
	m, _, _ := testModel(t)

	next, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	got := next.(Model)
	if got.mode != modeConfirmDelete {
		t.Fatal("d did not open the confirm-delete modal")
	}
	if got.deleteTarget == nil || got.deleteTarget.ID != "a1" {
		t.Fatalf("deleteTarget = %v, want the selected session a1", got.deleteTarget)
	}
}

func TestEnterConfirmDeleteNoSelectionIsNoop(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	m := New(t.TempDir(), reg, ws.ID)

	next, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	got := next.(Model)
	if got.mode != modeList {
		t.Fatal("d opened a modal with no session selected")
	}
	if got.deleteTarget != nil {
		t.Fatal("deleteTarget set with no session selected")
	}
}

func TestConfirmDeleteCancelLeavesSessionUnchanged(t *testing.T) {
	m, _, _ := testModel(t)
	next, _ := m.enterConfirmDelete()
	m = next.(Model)

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyEsc},
		{Type: tea.KeyRunes, Runes: []rune("n")},
		{Type: tea.KeyRunes, Runes: []rune("q")},
	} {
		next, _ := m.updateConfirmDelete(key)
		got := next.(Model)
		if got.mode != modeList {
			t.Fatalf("cancel key %v did not return to the list", key)
		}
		if got.deleteTarget != nil {
			t.Fatalf("cancel key %v left a delete target", key)
		}
	}
	if _, ok := m.reg.Session("a1"); !ok {
		t.Fatal("cancel removed the session record")
	}
}

func TestConfirmDeleteRemovesExitedSession(t *testing.T) {
	m, _, _ := testModel(t)
	m.cursor = 1 // select a2; a1 stays so the cursor has a neighbour to land on

	a2, _ := m.reg.Session("a2")
	a2.Status = registry.StatusExited // exited path runs no backend

	next, _ := m.enterConfirmDelete()
	m = next.(Model)
	if m.deleteTarget.ID != "a2" {
		t.Fatalf("deleteTarget = %q, want a2", m.deleteTarget.ID)
	}

	// y confirms directly regardless of which button is focused.
	next, cmd := m.updateConfirmDelete(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = next.(Model)
	if m.mode != modeList {
		t.Fatal("confirm did not return to the list")
	}
	if cmd == nil {
		t.Fatal("confirm produced no delete command")
	}

	// The exited-session path runs no backend; the command removes the record
	// and Saves. Feeding its result back into Update refreshes and clamps.
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.err != nil {
		t.Fatalf("delete reported error: %v", m.err)
	}
	if _, ok := m.reg.Session("a2"); ok {
		t.Fatal("delete left the session record in the registry")
	}
	if got := len(m.sessions()); got != 1 {
		t.Fatalf("wsA sessions after delete = %d, want 1", got)
	}
	if m.cursor < 0 || m.cursor >= len(m.sessions()) {
		t.Fatalf("cursor %d out of range after delete", m.cursor)
	}
}

func TestConfirmDeleteEnterDefaultsToCancel(t *testing.T) {
	m, _, _ := testModel(t)
	next, _ := m.enterConfirmDelete()
	m = next.(Model)

	// The cancel button starts focused, so a stray enter must not delete.
	next, cmd := m.updateConfirmDelete(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.mode != modeList {
		t.Fatal("enter did not close the modal")
	}
	if cmd != nil {
		t.Fatal("enter on the default (cancel) focus produced a delete command")
	}
	if _, ok := m.reg.Session("a1"); !ok {
		t.Fatal("enter on the default focus deleted the session")
	}
}

func TestConfirmDeleteFocusConfirmThenEnter(t *testing.T) {
	m, _, _ := testModel(t)
	a1, _ := m.reg.Session("a1")
	a1.Status = registry.StatusExited // exited path runs no backend

	next, _ := m.enterConfirmDelete()
	m = next.(Model)

	// Move focus onto the confirm button, then enter deletes.
	next, _ = m.updateConfirmDelete(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	next, cmd := m.updateConfirmDelete(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("enter on the confirm button produced no delete command")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if _, ok := m.reg.Session("a1"); ok {
		t.Fatal("tab+enter did not delete the session")
	}
}

func TestEmptyRegistryShowsBanner(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	m := New(t.TempDir(), reg, "")
	if m.activeID != "" {
		t.Fatalf("activeID = %q, want empty with no workspaces", m.activeID)
	}

	m.width, m.height = 80, 24
	view := m.View()
	if !strings.Contains(view, "No workspaces yet.") {
		t.Fatalf("view missing empty-state banner:\n%s", view)
	}
	if !strings.Contains(view, "workspace add") {
		t.Fatalf("banner does not point at workspace add:\n%s", view)
	}
}

// TestPreviewPreservesColor is the regression guard for issue #46 symptom 1:
// the cockpit preview must render the captured agent's ANSI colors, and the
// per-line width truncation must not slice through an escape sequence. The
// capture carries a truecolor SGR followed by long text that overflows the
// pane, so a correct (ANSI-aware) render keeps the full escape intact while a
// naive byte/rune truncation would cut it mid-code.
func TestPreviewPreservesColor(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	reg.AddSession(&registry.Session{
		ID: "s1", WorkspaceID: ws.ID, Branch: "feat/s1",
		Status: registry.StatusRunning,
	})

	m := New(t.TempDir(), reg, ws.ID)
	m.width, m.height = 100, 30
	m.preview = "\x1b[38;2;255;0;0mRED" +
		strings.Repeat("x", 200) + "\x1b[0m"

	out := m.View()
	if !strings.Contains(out, "\x1b[38;2;255;0;0m") {
		t.Fatalf("preview dropped the truecolor escape; "+
			"colors are stripped or corrupted.\n%q", out)
	}
}

func TestSelectedSessionEmpty(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	m := New(t.TempDir(), reg, ws.ID)

	if m.selectedSession() != nil {
		t.Fatal("selectedSession non-nil with no sessions")
	}
	if m.View() == "" {
		t.Fatal("empty workspace rendered nothing; want an empty-state banner")
	}
}
