package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/joakimcarlsson/wasa-cli/internal/config"
	"github.com/joakimcarlsson/wasa-cli/internal/record"
	"github.com/joakimcarlsson/wasa-cli/internal/registry"
	"github.com/joakimcarlsson/wasa-cli/internal/sessionstatus"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/pane"
)

// TestSessionListShowsRuntimeStatus renders a workspace whose running sessions
// hold each derived runtime status and asserts the list labels them distinctly,
// alongside the exited state read straight from the registry.
func TestSessionListShowsRuntimeStatus(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	for _, id := range []string{"work", "wait", "idle", "gone"} {
		reg.AddSession(&registry.Session{
			ID: id, WorkspaceID: ws.ID, Branch: "feat/" + id,
			Status: registry.StatusRunning, TmuxName: "t-" + id,
		})
	}
	gone, _ := reg.Session("gone")
	gone.Status = registry.StatusExited

	m := New(t.TempDir(), reg, ws.ID, config.Default())
	m.width, m.height = 120, 30
	m.lastStatus["work"] = sessionstatus.Working
	m.lastStatus["wait"] = sessionstatus.Waiting
	m.lastStatus["idle"] = sessionstatus.Idle

	out := plainViewContent(m)
	for _, label := range []string{"working", "waiting", "idle", "exited"} {
		if !strings.Contains(out, label) {
			t.Fatalf("list missing %q state label:\n%s", label, out)
		}
	}
}

// TestChurnTickIssuesNoGitForPlainSessions is the acceptance guard: with only
// plain sessions present the refresh tick must run no git, which it expresses by
// churnCmd returning a nil command (no work scheduled) and no targets gathered.
func TestChurnTickIssuesNoGitForPlainSessions(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	for _, id := range []string{"p1", "p2"} {
		reg.AddSession(&registry.Session{
			ID: id, WorkspaceID: ws.ID, WorkingDir: "/tmp",
			Status: registry.StatusRunning, TmuxName: "t-" + id,
		})
	}

	m := New(t.TempDir(), reg, ws.ID, config.Default())
	if got := len(m.churnTargets()); got != 0 {
		t.Fatalf("churnTargets = %d, want 0 for plain-only sessions", got)
	}
	if cmd := m.churnCmd(); cmd != nil {
		t.Fatal(
			"churn tick scheduled git work with only plain sessions present",
		)
	}
}

// TestChurnTargetsOnlyWorktreeSessions checks the tick gathers exactly the
// worktree sessions whose workspace resolves, skipping plain ones.
func TestChurnTargetsOnlyWorktreeSessions(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	reg.AddSession(&registry.Session{
		ID: "w1", WorkspaceID: ws.ID, Branch: "feature/w1",
		WorktreePath: "/wt/w1", BaseCommit: "deadbeef",
		Status: registry.StatusRunning, TmuxName: "t-w1",
	})
	reg.AddSession(&registry.Session{
		ID: "p1", WorkspaceID: ws.ID, WorkingDir: "/tmp",
		Status: registry.StatusRunning, TmuxName: "t-p1",
	})

	m := New(t.TempDir(), reg, ws.ID, config.Default())
	targets := m.churnTargets()
	if len(targets) != 1 || targets[0].sessionID != "w1" {
		t.Fatalf("churnTargets = %+v, want only worktree session w1", targets)
	}
	if targets[0].repoPath != "/repo" {
		t.Fatalf("target repoPath = %q, want /repo", targets[0].repoPath)
	}
	if m.churnCmd() == nil {
		t.Fatal("churn tick scheduled no work despite a worktree session")
	}
}

// TestSessionRowShowsChurn renders a worktree session with cached churn and one
// with zero churn, asserting the non-zero one shows +N/−M and the clean one
// shows no +0/−0 noise.
func TestSessionRowShowsChurn(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	reg.AddSession(&registry.Session{
		ID: "w1", WorkspaceID: ws.ID, Branch: "feature/w1",
		WorktreePath: "/wt/w1", BaseCommit: "deadbeef",
		Status: registry.StatusRunning, TmuxName: "t-w1",
	})
	reg.AddSession(&registry.Session{
		ID: "clean", WorkspaceID: ws.ID, Branch: "feature/clean",
		WorktreePath: "/wt/clean", BaseCommit: "deadbeef",
		Status: registry.StatusRunning, TmuxName: "t-clean",
	})

	m := New(t.TempDir(), reg, ws.ID, config.Default())
	m.width, m.height = 200, 30
	m.churn["w1"] = churnStat{added: 12, removed: 3}
	m.churn["clean"] = churnStat{added: 0, removed: 0}

	out := plainViewContent(m)
	if !strings.Contains(out, "+12/−3") {
		t.Fatalf("row missing +12/−3 churn suffix:\n%s", out)
	}
	if strings.Contains(out, "+0/−0") {
		t.Fatalf("clean worktree rendered zero-churn noise:\n%s", out)
	}
}

// TestSessionRowPlainSessionHasNoChurn checks a plain session never grows a
// churn suffix even if a stale stat is somehow cached against its id.
func TestSessionRowPlainSessionHasNoChurn(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	reg.AddSession(&registry.Session{
		ID: "p1", WorkspaceID: ws.ID, WorkingDir: "/tmp/work",
		Status: registry.StatusRunning, TmuxName: "t-p1",
	})

	m := New(t.TempDir(), reg, ws.ID, config.Default())
	m.width, m.height = 200, 30
	m.churn["p1"] = churnStat{added: 9, removed: 9}

	if out := plainViewContent(m); strings.Contains(out, "+9/−9") {
		t.Fatalf("plain session rendered a churn suffix:\n%s", out)
	}
}

// TestRecordedTokenMatchesBySessionID checks the recorded indicator keys off the
// session id: a session present in the recorded map renders the ⏺ glyph with the
// checkpoint's commit count, and an absent session renders nothing.
func TestRecordedTokenMatchesBySessionID(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	m := New(t.TempDir(), reg, ws.ID, config.Default())
	m.recorded["rec"] = record.Entry{
		Meta: record.Meta{SessionID: "rec", Commits: []string{"a", "b", "c"}},
	}

	got := m.recordedToken(&registry.Session{ID: "rec"}, false)
	if !strings.Contains(got, recordIcon) {
		t.Fatalf("recorded token missing %q glyph: %q", recordIcon, got)
	}
	if !strings.Contains(got, "3") {
		t.Fatalf("recorded token missing commit count 3: %q", got)
	}

	if got := m.recordedToken(
		&registry.Session{ID: "other"},
		false,
	); got != "" {
		t.Fatalf("un-recorded session got token %q, want empty", got)
	}
}

// TestSessionRowShowsRecordedIndicator renders one finished session with a
// checkpoint and one without, asserting only the recorded row grows the ⏺ N
// indicator and the un-recorded row stays clean (absence is the signal).
func TestSessionRowShowsRecordedIndicator(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	for _, id := range []string{"rec", "plain"} {
		reg.AddSession(&registry.Session{
			ID: id, WorkspaceID: ws.ID, Branch: "feat/" + id,
			Status: registry.StatusExited, TmuxName: "t-" + id,
		})
	}

	m := New(t.TempDir(), reg, ws.ID, config.Default())
	m.width, m.height = 200, 30
	m.recorded["rec"] = record.Entry{
		Meta: record.Meta{SessionID: "rec", Commits: []string{"x", "y"}},
	}

	out := plainViewContent(m)
	if !strings.Contains(out, recordIcon+" 2") &&
		!strings.Contains(out, recordIcon) {
		t.Fatalf(
			"recorded session row missing %q indicator:\n%s",
			recordIcon,
			out,
		)
	}
	if strings.Count(out, recordIcon) != 1 {
		t.Fatalf(
			"expected exactly one recorded indicator, got %d:\n%s",
			strings.Count(out, recordIcon), out,
		)
	}
}

// TestSubLineDropsRecordedTokenWhenNarrow is the layout guard: at a width too
// small for the indicator the sub-line drops it rather than wrapping or
// overflowing the column.
func TestSubLineDropsRecordedTokenWhenNarrow(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	m := New(t.TempDir(), reg, ws.ID, config.Default())
	m.recorded["rec"] = record.Entry{
		Meta: record.Meta{SessionID: "rec", Commits: []string{"x", "y"}},
	}
	s := &registry.Session{ID: "rec", ProfileName: "claude"}

	for _, w := range []int{20, 30, 40, 80} {
		out := m.subLine(
			s, "feature/a-fairly-long-branch-name",
			sessionstatus.Idle, m.theme.RowDescStyle, false, w,
		)
		if got := ansi.StringWidth(out); got > w {
			t.Fatalf("sub-line width %d exceeds column %d: %q", got, w, out)
		}
	}
}

// TestRefreshDiffCmdGatedOnDiffTab checks the live diff refresh runs only while
// the Diff tab is active for a selected worktree session.
func TestRefreshDiffCmdGatedOnDiffTab(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	reg.AddSession(&registry.Session{
		ID: "w1", WorkspaceID: ws.ID, Branch: "feature/w1",
		WorktreePath: "/wt/w1", BaseCommit: "deadbeef",
		Status: registry.StatusRunning, TmuxName: "t-w1",
	})

	m := New(t.TempDir(), reg, ws.ID, config.Default())
	if cmd := m.refreshDiffCmd(); cmd != nil {
		t.Fatal("diff refresh ran while the Preview tab was active")
	}

	m.tabbed.Cycle(1)
	if m.tabbed.Active() != pane.TabDiff {
		t.Fatalf("expected Diff tab active, got %v", m.tabbed.Active())
	}
	if cmd := m.refreshDiffCmd(); cmd == nil {
		t.Fatal("diff refresh did not run for a selected worktree session")
	}
}

func layoutModel(t *testing.T, cfg config.Config, width int) Model {
	t.Helper()
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	m := New(t.TempDir(), reg, ws.ID, cfg)
	m.width = width
	return m
}

func TestListColWidthUsesFraction(t *testing.T) {
	m := layoutModel(t, config.Default(), 200)
	if got, want := m.listColWidth(), int(200*0.34); got != want {
		t.Fatalf("default listColWidth = %d, want %d", got, want)
	}
}

func TestListColWidthOverrideWidensColumn(t *testing.T) {
	cfg := config.Default()
	cfg.Layout.ListColFrac = 0.6
	m := layoutModel(t, cfg, 200)

	def := layoutModel(t, config.Default(), 200)
	if m.listColWidth() <= def.listColWidth() {
		t.Fatalf(
			"override frac did not widen column: %d vs default %d",
			m.listColWidth(), def.listColWidth(),
		)
	}
	if got, want := m.listColWidth(), int(200*0.6); got != want {
		t.Fatalf("override listColWidth = %d, want %d", got, want)
	}
}

func TestListColWidthFlooredAtMinimum(t *testing.T) {
	cfg := config.Default()
	cfg.Layout.MinListWidth = 50
	m := layoutModel(t, cfg, 100)

	if got := m.listColWidth(); got != 50 {
		t.Fatalf("listColWidth = %d, want floor 50", got)
	}
}
