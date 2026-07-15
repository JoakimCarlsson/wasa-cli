package worktree

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestAddCollisionReturnsErrWorktreeExists(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	home := t.TempDir()
	repo := t.TempDir()
	initRepo(t, repo)

	m := New(repo, home, "demo")
	path, err := m.Add("task/again")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	_, err = m.Add("task/again")
	var exists *ErrWorktreeExists
	if !errors.As(err, &exists) {
		t.Fatalf("Add second time = %v, want *ErrWorktreeExists", err)
	}
	if exists.Branch != "task/again" || exists.Path != path {
		t.Fatalf(
			"ErrWorktreeExists = %+v, want branch %q path %q",
			exists, "task/again", path,
		)
	}

	// The collision must be resolvable: clearing the existing worktree lets
	// a retried Add succeed at the same path.
	if err := m.Remove(path, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := m.Add("task/again"); err != nil {
		t.Fatalf("Add after clearing collision: %v", err)
	}
}

func TestRemoveMissingWorktreeDir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	home := t.TempDir()
	repo := t.TempDir()
	initRepo(t, repo)

	m := New(repo, home, "demo")

	path, err := m.Add("task/pink")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Simulate a worktree directory that has already been deleted from disk
	// before teardown runs.
	if err := os.RemoveAll(path); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	// Removing the now-missing worktree by its absolute path must succeed
	// rather than mangling the path into a bogus branch segment.
	if err := m.Remove(path, true); err != nil {
		t.Fatalf("Remove missing worktree: %v", err)
	}

	list, err := m.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if containsBranch(list, "task/pink") {
		t.Fatalf("worktree still registered after remove: %+v", list)
	}

	// The absolute path must not be re-sanitized into a sibling directory
	// under the workspace's worktree root.
	root := filepath.Join(home, "worktrees", "demo")
	entries, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("ReadDir %q: %v", root, err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "worktrees") {
			t.Fatalf("mangled worktree dir created: %q", e.Name())
		}
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

func TestBranches(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	repo := t.TempDir()
	initRepo(t, repo)

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("branch", "feature/a")
	run("branch", "feature/b")

	branches, err := New(repo, t.TempDir(), "demo").Branches()
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}

	want := map[string]bool{"main": true, "feature/a": true, "feature/b": true}
	if len(branches) != len(want) {
		t.Fatalf("Branches = %v, want the three local branches", branches)
	}
	for _, b := range branches {
		if !want[b] {
			t.Errorf("unexpected branch %q in %v", b, branches)
		}
	}
}

func TestHeadSHA(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	repo := t.TempDir()
	initRepo(t, repo)

	sha, err := New(repo, t.TempDir(), "demo").HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	if len(sha) != 40 {
		t.Fatalf("HeadSHA = %q, want a 40-char object name", sha)
	}
}

func TestDiff(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	home := t.TempDir()
	repo := t.TempDir()
	initRepo(t, repo)

	m := New(repo, home, "demo")
	base, err := m.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	wt, err := m.Add("feature/diff")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// A fresh worktree against its base commit has nothing to show.
	res, err := m.Diff(wt, base)
	if err != nil {
		t.Fatalf("Diff (clean): %v", err)
	}
	if res.Text != "" || res.Added != 0 || res.Removed != 0 {
		t.Fatalf("clean worktree diff = %+v, want empty", res)
	}

	// An untracked file must appear (git add -N .) and count as additions.
	if err := os.WriteFile(
		filepath.Join(wt, "new.txt"), []byte("alpha\nbeta\n"), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	res, err = m.Diff(wt, base)
	if err != nil {
		t.Fatalf("Diff (changed): %v", err)
	}
	if !containsAll(res.Text, "new.txt", "+alpha", "+beta") {
		t.Fatalf("diff missing the untracked file content:\n%s", res.Text)
	}
	if res.Added != 2 {
		t.Fatalf("Added = %d, want 2", res.Added)
	}
	if res.Removed != 0 {
		t.Fatalf("Removed = %d, want 0", res.Removed)
	}
}

func TestDiffNumstat(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	home := t.TempDir()
	repo := t.TempDir()
	initRepo(t, repo)

	m := New(repo, home, "demo")
	base, err := m.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	wt, err := m.Add("feature/numstat")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// A clean worktree against its base commit churns nothing.
	added, removed, err := m.DiffNumstat(wt, base)
	if err != nil {
		t.Fatalf("DiffNumstat (clean): %v", err)
	}
	if added != 0 || removed != 0 {
		t.Fatalf("clean worktree churn = +%d/-%d, want +0/-0", added, removed)
	}

	// An untracked file is counted via git add -N .
	if err := os.WriteFile(
		filepath.Join(wt, "new.txt"), []byte("alpha\nbeta\ngamma\n"), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	added, removed, err = m.DiffNumstat(wt, base)
	if err != nil {
		t.Fatalf("DiffNumstat (changed): %v", err)
	}
	if added != 3 || removed != 0 {
		t.Fatalf("changed worktree churn = +%d/-%d, want +3/-0", added, removed)
	}

	// A binary-only change reports "-" in both numstat columns and counts zero.
	// It runs in its own worktree so the earlier intent-to-add of new.txt does
	// not bleed into the count.
	binWt, err := m.Add("feature/binary")
	if err != nil {
		t.Fatalf("Add (binary): %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(binWt, "blob.bin"), []byte{0x00, 0x01, 0x02, 0x00}, 0o644,
	); err != nil {
		t.Fatal(err)
	}
	added, removed, err = m.DiffNumstat(binWt, base)
	if err != nil {
		t.Fatalf("DiffNumstat (binary): %v", err)
	}
	if added != 0 || removed != 0 {
		t.Fatalf("binary-only churn = +%d/-%d, want +0/-0", added, removed)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
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
