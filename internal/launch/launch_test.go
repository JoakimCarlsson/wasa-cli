package launch

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/joakimcarlsson/wasa-cli/internal/hook"
	"github.com/joakimcarlsson/wasa-cli/internal/registry"
)

// fakeBackend is a SessionBackend that records the tmux names it was asked to
// kill and reports every session as not alive, so DeleteWorkspace's teardown of
// plain (worktree-less, branch-less) sessions runs without a real tmux server.
type fakeBackend struct{ killed []string }

func (f *fakeBackend) SpawnEnv(string, string, []string, ...string) error {
	return nil
}
func (f *fakeBackend) AttachCmd(string) (*exec.Cmd, error) { return nil, nil }
func (f *fakeBackend) Capture(string) (string, error)      { return "", nil }
func (f *fakeBackend) Has(string) (bool, error)            { return false, nil }
func (f *fakeBackend) List() ([]string, error)             { return nil, nil }
func (f *fakeBackend) Kill(name string) error {
	f.killed = append(f.killed, name)
	return nil
}

// TestDeleteWorkspaceRemovesSessionsAndWorkspace checks the cascade: every
// session owned by the target workspace is dropped and the workspace itself is
// removed, while a second workspace and its session are left untouched. The
// sessions are plain (no branch, no worktree) so teardown needs no git.
func TestDeleteWorkspaceRemovesSessionsAndWorkspace(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo-x", "", "repo-x")
	other, _ := reg.EnsureWorkspace("/repo-y", "", "repo-y")
	reg.AddSession(&registry.Session{
		ID: "p1", WorkspaceID: ws.ID, WorkingDir: "/tmp/a", TmuxName: "wasa_p1",
	})
	reg.AddSession(&registry.Session{
		ID: "p2", WorkspaceID: ws.ID, WorkingDir: "/tmp/b", TmuxName: "wasa_p2",
	})
	reg.AddSession(&registry.Session{
		ID: "k1", WorkspaceID: other.ID, WorkingDir: "/tmp/c",
		TmuxName: "wasa_k1",
	})

	n, err := DeleteWorkspace(reg, &fakeBackend{}, t.TempDir(), ws)
	if err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}
	if n != 2 {
		t.Fatalf("torn down = %d, want 2", n)
	}
	if _, ok := reg.Workspace(ws.ID); ok {
		t.Fatal("workspace still present after delete")
	}
	if _, ok := reg.Workspace(other.ID); !ok {
		t.Fatal("the other workspace was removed too")
	}
	sessions := reg.ListSessions()
	if len(sessions) != 1 || sessions[0].ID != "k1" {
		t.Fatalf("remaining sessions = %+v, want only k1", sessions)
	}
}

// recordingOps records what the create flow invokes so a test can assert which
// side effects ran. addWorktree and runHook fail loudly by default: a plain
// session must touch neither, so any call is a test failure rather than a
// silently recorded one.
type recordingOps struct {
	addCalled    bool
	addBranch    string
	applyRepo    string
	applyTree    string
	applyProfile registry.Profile
	portCalled   bool
	port         int
	hookCalled   bool
	hookCommand  string
	hookEnv      []string
	spawnDir     string
	spawnEnv     []string
	spawnName    string
	worktree     string
	baseCommit   string
	recordTree   string
}

func (o *recordingOps) ops() ops {
	return ops{
		addWorktree: func(_, _, _, branch string) (string, string, error) {
			o.addCalled = true
			o.addBranch = branch
			return o.worktree, o.baseCommit, nil
		},
		applyPaths: func(
			repoPath, worktreePath string, prof registry.Profile,
		) error {
			o.applyRepo = repoPath
			o.applyTree = worktreePath
			o.applyProfile = prof
			return nil
		},
		allocatePort: func() (int, error) {
			o.portCalled = true
			return o.port, nil
		},
		runHook: func(h hook.Hook) error {
			o.hookCalled = true
			o.hookCommand = h.Command
			o.hookEnv = h.Env
			return nil
		},
		spawn: func(name, dir string, env []string, _ string) error {
			o.spawnName = name
			o.spawnDir = dir
			o.spawnEnv = env
			return nil
		},
		prepareHooks: func(_, _, _ string, env []string) []string {
			return env
		},
		installRecordHooks: func(worktreePath, _ string) {
			o.recordTree = worktreePath
		},
	}
}

func testRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return reg
}

func TestCreateSessionPlainMakesNoWorktree(t *testing.T) {
	reg := testRegistry(t)
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")

	o := &recordingOps{}
	s, err := createSession(o.ops(), "/home", reg, ws, Params{
		Program:    "claude",
		WorkingDir: "/work/here",
	})
	if err != nil {
		t.Fatalf("createSession: %v", err)
	}

	if o.addCalled {
		t.Fatal("plain session called addWorktree")
	}
	if o.hookCalled {
		t.Fatal("plain session ran the post-worktree hook")
	}
	if o.recordTree != "" {
		t.Fatal("plain session installed worktree record hooks")
	}
	if s.Branch != "" || s.WorktreePath != "" {
		t.Fatalf("plain session recorded branch/worktree: %+v", s)
	}
	if s.WorkingDir != "/work/here" {
		t.Fatalf("WorkingDir = %q, want /work/here", s.WorkingDir)
	}
	if o.spawnDir != "/work/here" {
		t.Fatalf(
			"spawned in %q, want the working directory /work/here",
			o.spawnDir,
		)
	}
	if _, ok := reg.Session(s.ID); !ok {
		t.Fatal("plain session was not registered")
	}
}

func TestCreateSessionPlainInWorkspaceCarriesProfileEnv(t *testing.T) {
	reg := testRegistry(t)
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	ws.Profiles[0].Env = map[string]string{"FOO": "bar"}

	o := &recordingOps{}
	s, err := createSession(o.ops(), "/home", reg, ws, Params{
		Program:    "claude",
		WorkingDir: "/work",
	})
	if err != nil {
		t.Fatalf("createSession: %v", err)
	}

	if !slices.Contains(o.spawnEnv, "FOO=bar") {
		t.Fatalf(
			"plain session env = %v, want it to include FOO=bar",
			o.spawnEnv,
		)
	}
	if s.WorkspaceID != ws.ID {
		t.Fatalf(
			"WorkspaceID = %q, want the workspace it ran inside %q",
			s.WorkspaceID,
			ws.ID,
		)
	}
	if s.ProfileName != ws.Profiles[0].Name {
		t.Fatalf(
			"ProfileName = %q, want %q",
			s.ProfileName,
			ws.Profiles[0].Name,
		)
	}
}

func TestCreateSessionPlainNoWorkspaceHasNoEnv(t *testing.T) {
	reg := testRegistry(t)

	o := &recordingOps{}
	s, err := createSession(o.ops(), "/home", reg, nil, Params{
		Program:    "claude",
		WorkingDir: "/anywhere",
	})
	if err != nil {
		t.Fatalf("createSession outside a repository: %v", err)
	}

	if len(o.spawnEnv) != 0 {
		t.Fatalf(
			"session outside a workspace got env %v, want none",
			o.spawnEnv,
		)
	}
	if s.WorkspaceID != "" {
		t.Fatalf(
			"WorkspaceID = %q, want empty for a no-workspace session",
			s.WorkspaceID,
		)
	}
	if s.ProfileName != "" {
		t.Fatalf(
			"ProfileName = %q, want empty for a no-workspace session",
			s.ProfileName,
		)
	}
	if _, ok := reg.Session(s.ID); !ok {
		t.Fatal("no-workspace plain session was not registered")
	}
}

func TestCreateSessionWorktreeStillAddsAndHooks(t *testing.T) {
	reg := testRegistry(t)
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	ws.Profiles[0].PostWorktreeHook = "echo hi"

	o := &recordingOps{worktree: "/wt/feature-x", baseCommit: "abc123"}
	s, err := createSession(o.ops(), "/home", reg, ws, Params{
		Branch:  "feature/x",
		Program: "claude",
	})
	if err != nil {
		t.Fatalf("createSession: %v", err)
	}

	if !o.addCalled || o.addBranch != "feature/x" {
		t.Fatalf("worktree session did not add worktree for the branch: %+v", o)
	}
	if !o.hookCalled || o.hookCommand != "echo hi" {
		t.Fatalf("worktree session did not run the post-worktree hook: %+v", o)
	}
	if s.Branch != "feature/x" || s.WorktreePath != "/wt/feature-x" {
		t.Fatalf("worktree session record = %+v", s)
	}
	if s.BaseCommit != "abc123" {
		t.Fatalf("BaseCommit = %q, want the captured HEAD abc123", s.BaseCommit)
	}
	if o.spawnDir != "/wt/feature-x" {
		t.Fatalf("spawned in %q, want the worktree /wt/feature-x", o.spawnDir)
	}
	if o.recordTree != "/wt/feature-x" {
		t.Fatalf(
			"record hooks installed in %q, want the worktree /wt/feature-x",
			o.recordTree,
		)
	}
}

func TestCreateSessionWorktreeAppliesPaths(t *testing.T) {
	reg := testRegistry(t)
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	ws.Profiles[0].LinkPaths = []string{"node_modules"}
	ws.Profiles[0].CopyPaths = []string{".env"}

	o := &recordingOps{worktree: "/wt/feature-x"}
	if _, err := createSession(o.ops(), "/home", reg, ws, Params{
		Branch:  "feature/x",
		Program: "claude",
	}); err != nil {
		t.Fatalf("createSession: %v", err)
	}

	if o.applyRepo != "/repo" || o.applyTree != "/wt/feature-x" {
		t.Fatalf(
			"applyPaths called with repo=%q tree=%q, want /repo and the worktree",
			o.applyRepo,
			o.applyTree,
		)
	}
	if !slices.Equal(o.applyProfile.LinkPaths, []string{"node_modules"}) ||
		!slices.Equal(o.applyProfile.CopyPaths, []string{".env"}) {
		t.Fatalf(
			"applyPaths got profile %+v, want the link/copy paths",
			o.applyProfile,
		)
	}
}

func TestCreateSessionWorktreePortInjectsEnv(t *testing.T) {
	reg := testRegistry(t)
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	ws.Profiles[0].PortEnv = "PORT"

	o := &recordingOps{worktree: "/wt/feature-x", port: 54321}
	if _, err := createSession(o.ops(), "/home", reg, ws, Params{
		Branch:  "feature/x",
		Program: "claude",
	}); err != nil {
		t.Fatalf("createSession: %v", err)
	}

	if !o.portCalled {
		t.Fatal("PortEnv set but allocatePort was not called")
	}
	if !slices.Contains(o.spawnEnv, "PORT=54321") {
		t.Fatalf("spawn env = %v, want it to include PORT=54321", o.spawnEnv)
	}
	if !slices.Contains(o.hookEnv, "PORT=54321") {
		t.Fatalf("hook env = %v, want the port visible to the hook", o.hookEnv)
	}
}

func TestCreateSessionWorktreeNoPortEnvSkipsAllocation(t *testing.T) {
	reg := testRegistry(t)
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")

	o := &recordingOps{worktree: "/wt/feature-x", port: 54321}
	if _, err := createSession(o.ops(), "/home", reg, ws, Params{
		Branch:  "feature/x",
		Program: "claude",
	}); err != nil {
		t.Fatalf("createSession: %v", err)
	}

	if o.portCalled {
		t.Fatal("allocatePort called despite no PortEnv on the profile")
	}
	for _, e := range o.spawnEnv {
		if strings.HasPrefix(e, "PORT=") {
			t.Fatalf("spawn env unexpectedly carries a port: %q", e)
		}
	}
}

// pausedWorktreeSession registers a paused worktree session the resume tests
// rebuild: branch, old worktree path, preserved base commit and tmux name.
func pausedWorktreeSession(
	reg *registry.Registry, ws *registry.Workspace,
) *registry.Session {
	s := &registry.Session{
		ID:           "sess1",
		WorkspaceID:  ws.ID,
		ProfileName:  ws.Profiles[0].Name,
		Program:      "claude",
		Branch:       "feature/x",
		WorktreePath: "/wt/old",
		BaseCommit:   "base123",
		TmuxName:     "wasa_ws_sess1",
	}
	reg.AddSession(s)
	s.Status = registry.StatusPaused
	return s
}

func TestResumeSessionWorktreeRebuilds(t *testing.T) {
	reg := testRegistry(t)
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	ws.Profiles[0].PostWorktreeHook = "echo hi"
	ws.Profiles[0].PortEnv = "PORT"
	s := pausedWorktreeSession(reg, ws)

	o := &recordingOps{
		worktree:   "/wt/feature-x",
		baseCommit: "head999",
		port:       4242,
	}
	if err := resumeSession(o.ops(), "/home", reg, s); err != nil {
		t.Fatalf("resumeSession: %v", err)
	}

	if !o.addCalled || o.addBranch != "feature/x" {
		t.Fatalf("resume did not re-add the worktree for the branch: %+v", o)
	}
	if o.applyTree != "/wt/feature-x" {
		t.Fatalf(
			"bootstrap applied to %q, want the fresh worktree",
			o.applyTree,
		)
	}
	if !o.portCalled {
		t.Fatal("PortEnv set but the port was not re-allocated")
	}
	if !o.hookCalled || o.hookCommand != "echo hi" {
		t.Fatalf("resume did not re-run the post-worktree hook: %+v", o)
	}
	if o.recordTree != "/wt/feature-x" {
		t.Fatalf(
			"record hooks installed in %q, want the fresh worktree",
			o.recordTree,
		)
	}
	if o.spawnName != "wasa_ws_sess1" || o.spawnDir != "/wt/feature-x" {
		t.Fatalf(
			"spawned %q in %q, want the original tmux name in the worktree",
			o.spawnName,
			o.spawnDir,
		)
	}
	if s.Status != registry.StatusRunning {
		t.Fatalf(
			"status after resume = %q, want %q",
			s.Status,
			registry.StatusRunning,
		)
	}
	if s.WorktreePath != "/wt/feature-x" {
		t.Fatalf("WorktreePath = %q, want the re-created path", s.WorktreePath)
	}
	if s.BaseCommit != "base123" {
		t.Fatalf(
			"BaseCommit = %q, want the preserved base123 — resume must not reset it",
			s.BaseCommit,
		)
	}
}

func TestResumeSessionPlainRespawnsInWorkingDir(t *testing.T) {
	reg := testRegistry(t)
	s := &registry.Session{
		ID:         "plain1",
		Program:    "claude",
		WorkingDir: "/work/here",
		TmuxName:   "wasa__plain1",
	}
	reg.AddSession(s)
	s.Status = registry.StatusPaused

	o := &recordingOps{}
	if err := resumeSession(o.ops(), "/home", reg, s); err != nil {
		t.Fatalf("resumeSession: %v", err)
	}

	if o.addCalled || o.hookCalled {
		t.Fatalf("plain resume touched worktree machinery: %+v", o)
	}
	if o.spawnDir != "/work/here" {
		t.Fatalf("spawned in %q, want the working directory", o.spawnDir)
	}
	if s.Status != registry.StatusRunning {
		t.Fatalf(
			"status after resume = %q, want %q",
			s.Status,
			registry.StatusRunning,
		)
	}
}

func TestResumeSessionRunningIsRejected(t *testing.T) {
	reg := testRegistry(t)
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	s := pausedWorktreeSession(reg, ws)
	s.Status = registry.StatusRunning

	o := &recordingOps{}
	if err := resumeSession(o.ops(), "/home", reg, s); err == nil {
		t.Fatal("resumeSession accepted an already-running session")
	}
	if o.addCalled {
		t.Fatal("addWorktree called for an already-running session")
	}
}

// TestPauseSessionPlainMarksPaused drives PauseSession over a plain session with
// no workspace: only its tmux is probed (dead, so nothing is killed), and the
// session ends paused with its record retained.
func TestPauseSessionPlainMarksPaused(t *testing.T) {
	reg := testRegistry(t)
	s := &registry.Session{
		ID:         "plain1",
		Program:    "claude",
		WorkingDir: "/work",
		TmuxName:   "wasa__plain1",
	}
	reg.AddSession(s)

	be := &fakeBackend{}
	if err := PauseSession(reg, be, t.TempDir(), s, false); err != nil {
		t.Fatalf("PauseSession: %v", err)
	}
	if s.Status != registry.StatusPaused {
		t.Fatalf("status = %q, want %q", s.Status, registry.StatusPaused)
	}
	if _, ok := reg.Session("plain1"); !ok {
		t.Fatal("session record removed on pause; it must be retained")
	}
}

func TestCreateSessionWorktreeRequiresWorkspace(t *testing.T) {
	reg := testRegistry(t)

	o := &recordingOps{}
	_, err := createSession(o.ops(), "/home", reg, nil, Params{
		Branch:  "feature/x",
		Program: "claude",
	})
	if err == nil {
		t.Fatal("worktree session with no workspace returned nil error")
	}
	if o.addCalled {
		t.Fatal("addWorktree called despite the missing workspace")
	}
}
