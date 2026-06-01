package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joakimcarlsson/wasa/internal/config"
	"github.com/joakimcarlsson/wasa/internal/registry"
	"github.com/joakimcarlsson/wasa/internal/worktree"
)

func TestDiffBodyPlainSessionExplains(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	reg.AddSession(&registry.Session{
		ID: "p1", WorkspaceID: ws.ID, WorkingDir: "/work",
		TmuxName: "wasa_x_p1", Status: registry.StatusRunning,
	})
	m := New(t.TempDir(), reg, ws.ID, config.Default())
	m.width, m.height = 120, 30
	m.pane = paneDiff

	if cmd := m.ensureDiffCmd(); cmd == nil {
		t.Fatal("plain session should still produce a load command")
	}
	w, h := m.rightPaneSize()
	body := m.diffBody(w, h)
	if !strings.Contains(body, "only available for worktree sessions") {
		t.Fatalf("plain session diff body = %q", body)
	}
}

func TestDiffBodyLoadingBeforeCompute(t *testing.T) {
	m := diffWorktreeModel(t)
	m.pane = paneDiff
	w, h := m.rightPaneSize()
	if body := m.diffBody(w, h); !strings.Contains(body, "Loading diff") {
		t.Fatalf("uncomputed worktree diff body = %q", body)
	}
}

func TestEnsureDiffCmdGuardsAlreadyLoaded(t *testing.T) {
	m := diffWorktreeModel(t)
	m.pane = paneDiff
	m.diffSID = m.selectedSession().ID // pretend it is already loaded
	if cmd := m.ensureDiffCmd(); cmd != nil {
		t.Fatal("ensureDiffCmd recomputed an already-loaded diff")
	}
}

func TestApplyDiffDropsStaleDelivery(t *testing.T) {
	m := diffWorktreeModel(t)
	m.applyDiff(diffMsg{sessionID: "not-the-selected-one", text: "x", added: 9})
	if m.diffSID != "" || m.diffAdded != 0 {
		t.Fatalf("stale diff was applied: sid=%q added=%d",
			m.diffSID, m.diffAdded)
	}
}

// TestColorizeDiffPreservesContent checks the line-by-line pass keeps every
// line and its order intact. It cannot assert ANSI: lipgloss renders plain under
// the test runner's no-color profile, so the colours are verified manually.
func TestColorizeDiffPreservesContent(t *testing.T) {
	in := "@@ -1 +1 @@\n+added\n-removed\n context"
	out := colorizeDiff(testTheme, in)
	for _, want := range []string{"@@ -1 +1 @@", "added", "removed", " context"} {
		if !strings.Contains(out, want) {
			t.Fatalf("colorizeDiff dropped %q from:\n%s", want, out)
		}
	}
	if got := strings.Count(out, "\n"); got != 3 {
		t.Fatalf("colorizeDiff changed line count: %d newlines, want 3", got)
	}
}

// TestDiffTabShowsWorktreeChanges drives the full diff pipeline against a real
// git repository: it records a base commit, makes a change in the worktree, then
// computes and renders the diff exactly as the Diff tab does.
func TestDiffTabShowsWorktreeChanges(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	home := t.TempDir()
	repo := t.TempDir()
	gitInit(t, repo)

	wm := worktree.New(repo, home, "ws")
	base, err := wm.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	wt, err := wm.Add("feature/x")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(wt, "f.txt"), []byte("one\ntwo\n"), 0o644,
	); err != nil {
		t.Fatal(err)
	}

	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace(repo, "", "ws")
	reg.AddSession(&registry.Session{
		ID: "d1", WorkspaceID: ws.ID, Branch: "feature/x",
		WorktreePath: wt, BaseCommit: base,
		TmuxName: "wasa_x_d1", Status: registry.StatusRunning,
	})
	m := New(t.TempDir(), reg, ws.ID, config.Default())
	m.width, m.height = 120, 30
	m.pane = paneDiff

	cmd := m.ensureDiffCmd()
	if cmd == nil {
		t.Fatal("expected a diff command for a worktree session")
	}
	msg, ok := cmd().(diffMsg)
	if !ok || msg.err != nil {
		t.Fatalf("diff command failed: %+v", msg)
	}
	m.applyDiff(msg)

	if m.diffAdded != 2 {
		t.Fatalf("diffAdded = %d, want 2", m.diffAdded)
	}
	w, h := m.rightPaneSize()
	body := m.diffBody(w, h)
	if !strings.Contains(body, "2 additions(+)") {
		t.Fatalf("diff body missing summary line:\n%s", body)
	}
	if !strings.Contains(body, "f.txt") {
		t.Fatalf("diff body missing the changed file:\n%s", body)
	}
}

// diffWorktreeModel builds a model with a single selected worktree session that
// carries a base commit, sized for the full frame. It does not compute a diff.
func diffWorktreeModel(t *testing.T) Model {
	t.Helper()
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	reg.AddSession(&registry.Session{
		ID: "w1", WorkspaceID: ws.ID, Branch: "feature/w1",
		WorktreePath: "/wt/w1", BaseCommit: "deadbeef",
		TmuxName: "wasa_x_w1", Status: registry.StatusRunning,
	})
	m := New(t.TempDir(), reg, ws.ID, config.Default())
	m.width, m.height = 120, 30
	return m
}

func gitInit(t *testing.T, dir string) {
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
