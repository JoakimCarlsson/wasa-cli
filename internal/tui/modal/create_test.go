package modal

import (
	"os/exec"
	"slices"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/joakimcarlsson/wasa-cli/internal/config"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/theme"
	"github.com/joakimcarlsson/wasa-cli/internal/worktree"
)

// testTheme is the resolved default theme, used by the modal tests that build a
// form or editor directly.
func testTheme() theme.Theme {
	return theme.NewTheme(config.Default().Theme)
}

// emits reports whether cmd runs and yields a message of type T.
func emits[T tea.Msg](cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(T)
	return ok
}

// TestFormBranchDisabledWithoutRepo checks that with no directory chosen the
// Branch field is skipped in tab order and a stray branch value is ignored, so a
// plain session is produced.
func TestFormBranchDisabledWithoutRepo(t *testing.T) {
	f := NewCreateForm(testTheme(), nil, "")
	if f.BranchEnabled() {
		t.Fatal("branch should be disabled before a directory is chosen")
	}

	f.focusNext()
	if f.focus == fieldBranch {
		t.Errorf("tab landed on the disabled Branch field")
	}

	f.inputs[fieldBranch].SetValue("feature/x")
	f.inputs[fieldDir].SetValue("/tmp/here")
	p := f.Params()
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

	f := NewCreateForm(testTheme(), nil, "")
	f.SetDir(repo)
	if !f.BranchEnabled() {
		t.Fatal(
			"branch should be enabled for a directory inside a git repository",
		)
	}

	f.focusNext()
	if f.focus != fieldBranch {
		t.Fatalf(
			"focus = %d after tab, want Branch field %d",
			f.focus,
			fieldBranch,
		)
	}

	f.inputs[fieldBranch].SetValue("feature/x")
	if p := f.Params(); p.Branch != "feature/x" {
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

	f := NewCreateForm(testTheme(), nil, "")
	if f.BranchEnabled() {
		t.Fatal("branch should be disabled before a directory is chosen")
	}

	f.SetDir(repo)
	if !f.BranchEnabled() {
		t.Fatal(
			"branch should be enabled for a directory inside a git repository",
		)
	}
	branches, err := worktree.New(f.BranchRepo, "", "").Branches()
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	if !slices.Contains(branches, "feature-x") ||
		!slices.Contains(branches, "feature-y") {
		t.Errorf("branches = %v, want feature-x and feature-y", branches)
	}

	f.SetDir(plain)
	if f.BranchEnabled() {
		t.Fatal("branch should be disabled for a non-git directory")
	}
}

// TestFormWorkspaceModeDropsDirectory checks that inside a workspace the form
// has no Directory field: focus starts on Branch, tab never lands on the dropped
// Directory field, and a plain session runs in the workspace's repository root
// rather than a picked path.
func TestFormWorkspaceModeDropsDirectory(t *testing.T) {
	const repo = "/repos/acme"
	f := NewCreateForm(testTheme(), []string{"default"}, repo)

	if f.dirEnabled() {
		t.Fatal("Directory field should be dropped inside a workspace")
	}
	if f.focus != fieldBranch {
		t.Fatalf(
			"focus = %d on open, want Branch field %d",
			f.focus,
			fieldBranch,
		)
	}

	f.focusNext()
	if f.focus == fieldDir {
		t.Error("tab landed on the dropped Directory field")
	}

	f.setFocus(fieldProgram)
	f.focusPrev()
	if f.focus == fieldDir {
		t.Error("shift+tab landed on the dropped Directory field")
	}

	f.inputs[fieldDir].SetValue("/somewhere/else")
	if p := f.Params(); p.WorkingDir != repo {
		t.Errorf(
			"WorkingDir = %q, want the workspace repo %q",
			p.WorkingDir,
			repo,
		)
	}
}

// TestFormWorkspaceModeBranchTargetsWorkspaceRepo checks that inside a workspace
// the Branch field is always enabled and resolves against the workspace's
// repository, independent of anything typed into the (dropped) Directory field.
func TestFormWorkspaceModeBranchTargetsWorkspaceRepo(t *testing.T) {
	const repo = "/repos/acme"
	f := NewCreateForm(testTheme(), []string{"default"}, repo)

	if !f.BranchEnabled() {
		t.Fatal("branch should be enabled inside a workspace")
	}
	if f.BranchRepo != repo {
		t.Fatalf(
			"BranchRepo = %q, want the workspace repo %q",
			f.BranchRepo,
			repo,
		)
	}

	f.inputs[fieldDir].SetValue("/somewhere/else")
	f.SyncBranchRepo()
	if f.BranchRepo != repo {
		t.Fatalf(
			"BranchRepo = %q after a stray Directory value, want %q",
			f.BranchRepo, repo,
		)
	}

	f.inputs[fieldBranch].SetValue("feature/x")
	p := f.Params()
	if p.Branch != "feature/x" {
		t.Errorf("params.Branch = %q, want feature/x", p.Branch)
	}
	if p.WorkingDir != "" {
		t.Errorf("worktree session leaked a WorkingDir: %q", p.WorkingDir)
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

// TestFormAutonomousInjectsFlag checks that with a known agent selected, toggling
// autonomous on bakes that agent's skip-permissions flag into the spawned program.
func TestFormAutonomousInjectsFlag(t *testing.T) {
	f := NewCreateForm(testTheme(), nil, "")
	f.inputs[fieldProgram].SetValue("claude")

	if !f.autonomousEnabled() {
		t.Fatal("autonomous should be enabled for claude")
	}
	f.toggleAutonomous()
	if !f.autonomous {
		t.Fatal("toggleAutonomous did not enable the toggle")
	}

	want := "claude --dangerously-skip-permissions"
	if p := f.Params(); p.Program != want {
		t.Errorf("params.Program = %q, want %q", p.Program, want)
	}
}

// TestFormAutonomousDisabledForShell checks that an unknown/shell program leaves
// the toggle disabled, skipped in tab order, and never injects a flag even if the
// toggle state was set on.
func TestFormAutonomousDisabledForShell(t *testing.T) {
	f := NewCreateForm(testTheme(), nil, "")
	f.inputs[fieldProgram].SetValue("/bin/bash")

	if f.autonomousEnabled() {
		t.Fatal("autonomous should be disabled for the shell")
	}

	f.setFocus(fieldProgram)
	f.focusNext()
	if f.focus == fieldAutonomous {
		t.Error("tab landed on the disabled Autonomous field")
	}

	f.autonomous = true
	if p := f.Params(); p.Program != "/bin/bash" {
		t.Errorf("params.Program = %q, want the bare shell", p.Program)
	}
}

// TestFormAutonomousDropsFlagWhenProgramChanges checks that turning the toggle on
// for a known agent and then switching to the shell omits the flag, since the
// toggle is gated on the current program supporting it.
func TestFormAutonomousDropsFlagWhenProgramChanges(t *testing.T) {
	f := NewCreateForm(testTheme(), nil, "")
	f.inputs[fieldProgram].SetValue("claude")
	f.toggleAutonomous()

	f.inputs[fieldProgram].SetValue("/bin/bash")
	if p := f.Params(); p.Program != "/bin/bash" {
		t.Errorf("params.Program = %q, want the bare shell", p.Program)
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

	f := NewCreateForm(testTheme(), nil, "")
	ctrlF := tea.KeyMsg{Type: tea.KeyCtrlF}

	if _, cmd := f.Update(ctrlF); !emits[FormPickDirMsg](cmd) {
		t.Errorf("on Directory, ctrl+f did not emit FormPickDirMsg")
	}

	f.SetDir(repo)
	f.setFocus(fieldBranch)
	if _, cmd := f.Update(ctrlF); !emits[FormPickBranchMsg](cmd) {
		t.Errorf("on Branch, ctrl+f did not emit FormPickBranchMsg")
	}
}
