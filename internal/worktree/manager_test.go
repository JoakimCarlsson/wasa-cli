package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestManagerAddListRemove(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	home := t.TempDir()
	repo := t.TempDir()
	initRepo(t, repo)

	m := New(repo, home, "demo")

	path, err := m.Add("feature/demo")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	wantPath := filepath.Join(home, "worktrees", "demo", "feature-demo")
	if path != wantPath {
		t.Fatalf("Add path = %q, want %q", path, wantPath)
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("worktree dir not created at %q: %v", path, err)
	}

	list, err := m.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !containsBranch(list, "feature/demo") {
		t.Fatalf("List missing feature/demo: %+v", list)
	}

	if err := m.Remove("feature/demo", false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("worktree dir still present after remove: %v", err)
	}
}

func TestDeleteBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	home := t.TempDir()
	repo := t.TempDir()
	initRepo(t, repo)

	m := New(repo, home, "demo")
	if _, err := m.Add("feature/gone"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := m.Remove("feature/gone", false); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if err := m.DeleteBranch("feature/gone", true); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
	if branchPresent(t, repo, "feature/gone") {
		t.Fatal("branch still present after force DeleteBranch")
	}
}

func branchPresent(t *testing.T, repo, branch string) bool {
	t.Helper()
	cmd := exec.Command("git", "-C", repo, "branch", "--list", branch)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git branch --list: %v", err)
	}
	return len(out) > 0
}

func containsBranch(list []Worktree, branch string) bool {
	for _, w := range list {
		if w.Branch == branch {
			return true
		}
	}
	return false
}

func initRepo(t *testing.T, dir string) {
	t.Helper()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	run("commit", "--allow-empty", "-m", "initial")
}
