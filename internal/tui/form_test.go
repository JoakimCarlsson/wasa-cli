package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestFormBranchDisabledWithoutRepo checks that with no repository the Branch
// field is skipped in tab order and a stray branch value is ignored, so a plain
// session is produced.
func TestFormBranchDisabledWithoutRepo(t *testing.T) {
	f := newCreateForm(nil, "")
	if f.branchEnabled() {
		t.Fatal("branch should be disabled without a repo path")
	}

	f.focusNext() // from Directory
	if f.focus == fieldBranch {
		t.Errorf("tab landed on the disabled Branch field")
	}

	f.inputs[fieldBranch].SetValue("feature/x")
	f.inputs[fieldDir].SetValue("/tmp/here")
	p := f.params()
	if p.Branch != "" {
		t.Errorf("disabled branch leaked into params: %q", p.Branch)
	}
	if p.WorkingDir != "/tmp/here" {
		t.Errorf(
			"WorkingDir = %q, want the plain-session directory",
			p.WorkingDir,
		)
	}
}

// TestFormBranchEnabledWithRepo checks that with a repo path the Branch field is
// reachable by tab and a branch value selects a worktree session.
func TestFormBranchEnabledWithRepo(t *testing.T) {
	f := newCreateForm(nil, "/repo")
	if !f.branchEnabled() {
		t.Fatal("branch should be enabled with a repo path")
	}

	f.focusNext() // Directory -> Branch
	if f.focus != fieldBranch {
		t.Fatalf(
			"focus = %d after tab, want Branch field %d",
			f.focus,
			fieldBranch,
		)
	}

	f.inputs[fieldBranch].SetValue("feature/x")
	if p := f.params(); p.Branch != "feature/x" {
		t.Errorf("params.Branch = %q, want feature/x", p.Branch)
	}
}

// TestFormCtrlFRoutesByField checks that ctrl+f opens the directory browser from
// the Directory field and the branch picker from the Branch field.
func TestFormCtrlFRoutesByField(t *testing.T) {
	f := newCreateForm(nil, "/repo")
	ctrlF := tea.KeyMsg{Type: tea.KeyCtrlF}

	if _, result, _ := f.update(ctrlF); result != formPickDir {
		t.Errorf("on Directory, ctrl+f result = %v, want formPickDir", result)
	}

	f.setFocus(fieldBranch)
	if _, result, _ := f.update(ctrlF); result != formPickBranch {
		t.Errorf("on Branch, ctrl+f result = %v, want formPickBranch", result)
	}
}
