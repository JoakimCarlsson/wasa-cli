package tui

import (
	"os/exec"
	"slices"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestFormBranchDisabledWithoutRepo checks that with no directory chosen the
// Branch field is skipped in tab order and a stray branch value is ignored, so a
// plain session is produced.
func TestFormBranchDisabledWithoutRepo(t *testing.T) {
	f := newCreateForm(nil)
	if f.branchEnabled() {
		t.Fatal("branch should be disabled before a directory is chosen")
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

// TestFormBranchEnabledWithRepo checks that once a Directory inside a git
// repository is chosen the Branch field is reachable by tab and a branch value
// selects a worktree session.
func TestFormBranchEnabledWithRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	repo := t.TempDir()
	initRepo(t, repo)

	f := newCreateForm(nil)
	f.setDir(repo)
	if !f.branchEnabled() {
		t.Fatal(
			"branch should be enabled for a directory inside a git repository",
		)
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

// TestFormBranchRepoFollowsChosenDirectory checks that the Branch field keys off
// the chosen Directory: a directory inside a git repository enables the field and
// lists that repository's branches, while a plain (non-git) directory disables it
// — independent of the launch context, which here has no repository at all.
func TestFormBranchRepoFollowsChosenDirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	repo := t.TempDir()
	initRepo(t, repo)
	runGit(t, repo, "branch", "feature-x")
	runGit(t, repo, "branch", "feature-y")

	plain := t.TempDir()

	f := newCreateForm(nil)
	if f.branchEnabled() {
		t.Fatal("branch should be disabled before a directory is chosen")
	}

	f.setDir(repo)
	if !f.branchEnabled() {
		t.Fatal(
			"branch should be enabled for a directory inside a git repository",
		)
	}
	branches := repoBranches(f.branchRepo)
	if !slices.Contains(branches, "feature-x") ||
		!slices.Contains(branches, "feature-y") {
		t.Errorf("branches = %v, want feature-x and feature-y", branches)
	}

	f.setDir(plain)
	if f.branchEnabled() {
		t.Fatal("branch should be disabled for a non-git directory")
	}
}

// initRepo initialises a git repository with one commit at dir.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "test")
	runGit(t, dir, "commit", "--allow-empty", "-m", "initial")
}

// runGit runs a git command in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// TestFormCtrlFRoutesByField checks that ctrl+f opens the directory browser from
// the Directory field and the branch picker from the Branch field.
func TestFormCtrlFRoutesByField(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	repo := t.TempDir()
	initRepo(t, repo)

	f := newCreateForm(nil)
	ctrlF := tea.KeyMsg{Type: tea.KeyCtrlF}

	if _, result, _ := f.update(ctrlF); result != formPickDir {
		t.Errorf("on Directory, ctrl+f result = %v, want formPickDir", result)
	}

	f.setDir(repo)
	f.setFocus(fieldBranch)
	if _, result, _ := f.update(ctrlF); result != formPickBranch {
		t.Errorf("on Branch, ctrl+f result = %v, want formPickBranch", result)
	}
}
