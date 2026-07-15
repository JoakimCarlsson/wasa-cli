package component

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestBranchPickerEmptyQueryKeepsOrder(t *testing.T) {
	p := NewBranchPicker(
		testTheme(),
		[]string{"main", "feature/x", "fix/y"},
		60,
		14,
	)
	if len(p.matches) != 3 {
		t.Fatalf("got %d matches, want 3", len(p.matches))
	}
	if p.matches[0].name != "main" {
		t.Errorf(
			"first match = %q, want incoming order (main)",
			p.matches[0].name,
		)
	}
}

func TestBranchPickerFilters(t *testing.T) {
	p := NewBranchPicker(
		testTheme(),
		[]string{"main", "feature/login", "feature/logout"},
		60,
		14,
	)

	p, _ = p.Update(keyRunes("logi"))

	if len(p.matches) != 1 || p.matches[0].name != "feature/login" {
		t.Fatalf("matches = %v, want only feature/login", p.matches)
	}
}

func TestBranchPickerChoosesSelected(t *testing.T) {
	p := NewBranchPicker(testTheme(), []string{"main", "develop"}, 60, 14)

	p, _ = p.Update(keyDown()) // onto develop
	p, cmd := p.Update(keyEnter())

	msg, ok := runCmd(cmd).(BranchChosenMsg)
	if !ok {
		t.Fatalf("enter emitted %T, want BranchChosenMsg", runCmd(cmd))
	}
	if msg.Branch != "develop" {
		t.Errorf("chosen branch = %q, want develop", msg.Branch)
	}
	if p.Chosen != "develop" {
		t.Errorf("chosen = %q, want develop", p.Chosen)
	}
}

// TestBranchPickerCreatesTypedBranch checks that with a query matching nothing,
// enter chooses the typed text so a worktree can be made on a new branch.
func TestBranchPickerCreatesTypedBranch(t *testing.T) {
	p := NewBranchPicker(testTheme(), []string{"main"}, 60, 14)

	p, _ = p.Update(keyRunes("feature/new"))
	if len(p.matches) != 0 {
		t.Fatalf("expected no matches for a novel name, got %v", p.matches)
	}
	p, cmd := p.Update(keyEnter())

	msg, ok := runCmd(cmd).(BranchChosenMsg)
	if !ok {
		t.Fatalf("enter emitted %T, want BranchChosenMsg", runCmd(cmd))
	}
	if msg.Branch != "feature/new" {
		t.Errorf("chosen branch = %q, want the typed feature/new", msg.Branch)
	}
	if p.Chosen != "feature/new" {
		t.Errorf("chosen = %q, want the typed feature/new", p.Chosen)
	}
}

func TestBranchPickerEscCancels(t *testing.T) {
	p := NewBranchPicker(testTheme(), []string{"main"}, 60, 14)
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if _, ok := runCmd(cmd).(BranchCancelledMsg); !ok {
		t.Errorf("esc emitted %T, want BranchCancelledMsg", runCmd(cmd))
	}
}
