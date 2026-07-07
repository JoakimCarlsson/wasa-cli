package finish

import (
	"os"
	"os/exec"
	"testing"

	"github.com/joakimcarlsson/wasa-cli/internal/record"
	"github.com/joakimcarlsson/wasa-cli/internal/registry"
	"github.com/joakimcarlsson/wasa-cli/internal/worktree"
)

// realOps backs the worktree and branch operations with a real git repository
// and stubs tmux as already dead, so the teardown runs end to end against git
// without needing a tmux server.
type realOps struct {
	wt   *worktree.Manager
	home string
}

func (o realOps) TmuxAlive(string) (bool, error) { return false, nil }
func (o realOps) KillTmux(string) error          { return nil }

func (o realOps) RemoveWorktree(
	p string,
	f bool,
) error {
	return o.wt.Remove(p, f)
}

func (o realOps) DeleteBranch(
	b string,
) error {
	return o.wt.DeleteBranch(b, true)
}

func (o realOps) RecordCheckpoint(s *registry.Session) {
	record.FinishSession(o.home, o.wt.RepoDir, s)
}

func TestSessionAgainstRealRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	home := t.TempDir()
	repo := t.TempDir()
	initRepo(t, repo)

	m := worktree.New(repo, home, "demo")
	path, err := m.Add("feature/finish")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("worktree not created: %v", err)
	}

	s := &registry.Session{
		ID:           "sess1",
		Branch:       "feature/finish",
		WorktreePath: path,
	}
	res, err := Session(realOps{wt: m, home: home}, s, false)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if !res.RemovedWorktree || !res.DeletedBranch {
		t.Fatalf("teardown incomplete: %+v", res)
	}

	entries, err := record.List(repo)
	if err != nil || len(entries) != 1 {
		t.Fatalf("checkpoints after finish = %v, %v; want 1", entries, err)
	}
	if entries[0].Meta.SessionID != "sess1" {
		t.Fatalf(
			"checkpoint session = %q, want sess1",
			entries[0].Meta.SessionID,
		)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("worktree dir still present: %v", err)
	}
	if branchPresent(t, repo, "feature/finish") {
		t.Fatal("branch still present after finish")
	}
}

func branchPresent(t *testing.T, repo, branch string) bool {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "branch", "--list", branch).
		Output()
	if err != nil {
		t.Fatalf("git branch --list: %v", err)
	}
	return len(out) > 0
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
