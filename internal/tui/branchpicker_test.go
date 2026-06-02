package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestBranchPickerEmptyQueryKeepsOrder(t *testing.T) {
	p := newBranchPicker(testTheme, []string{"main", "feature/x", "fix/y"}, 60, 14)
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
	p := newBranchPicker(testTheme,
		[]string{"main", "feature/login", "feature/logout"},
		60,
		14,
	)

	p, _ = p.update(keyRunes("logi"))

	if len(p.matches) != 1 || p.matches[0].name != "feature/login" {
		t.Fatalf("matches = %v, want only feature/login", p.matches)
	}
}

func TestBranchPickerChoosesSelected(t *testing.T) {
	p := newBranchPicker(testTheme, []string{"main", "develop"}, 60, 14)

	p, _ = p.update(keyDown())
	p, cmd := p.update(keyEnter())

	if _, ok := runMsg(cmd).(branchPickedMsg); !ok {
		t.Fatal("enter did not report a chosen branch")
	}
	if p.chosen != "develop" {
		t.Errorf("chosen = %q, want develop", p.chosen)
	}
}

// TestBranchPickerCreatesTypedBranch checks that with a query matching nothing,
// enter chooses the typed text so a worktree can be made on a new branch.
func TestBranchPickerCreatesTypedBranch(t *testing.T) {
	p := newBranchPicker(testTheme, []string{"main"}, 60, 14)

	p, _ = p.update(keyRunes("feature/new"))
	if len(p.matches) != 0 {
		t.Fatalf("expected no matches for a novel name, got %v", p.matches)
	}
	p, cmd := p.update(keyEnter())

	if _, ok := runMsg(cmd).(branchPickedMsg); !ok {
		t.Fatal("enter did not report a chosen branch")
	}
	if p.chosen != "feature/new" {
		t.Errorf("chosen = %q, want the typed feature/new", p.chosen)
	}
}

func TestBranchPickerEscCancels(t *testing.T) {
	p := newBranchPicker(testTheme, []string{"main"}, 60, 14)
	_, cmd := p.update(tea.KeyMsg{Type: tea.KeyEsc})
	if _, ok := runMsg(cmd).(branchCancelledMsg); !ok {
		t.Error("esc did not report a cancellation")
	}
}
