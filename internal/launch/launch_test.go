package launch

import (
	"slices"
	"testing"

	"github.com/joakimcarlsson/wasa/internal/hook"
	"github.com/joakimcarlsson/wasa/internal/registry"
)

// recordingOps records what the create flow invokes so a test can assert which
// side effects ran. addWorktree and runHook fail loudly by default: a plain
// session must touch neither, so any call is a test failure rather than a
// silently recorded one.
type recordingOps struct {
	addCalled   bool
	addBranch   string
	hookCalled  bool
	hookCommand string
	spawnDir    string
	spawnEnv    []string
	spawnName   string
	worktree    string
}

func (o *recordingOps) ops() ops {
	return ops{
		addWorktree: func(_, _, _, branch string) (string, error) {
			o.addCalled = true
			o.addBranch = branch
			return o.worktree, nil
		},
		runHook: func(h hook.Hook) error {
			o.hookCalled = true
			o.hookCommand = h.Command
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

	o := &recordingOps{worktree: "/wt/feature-x"}
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
	if o.spawnDir != "/wt/feature-x" {
		t.Fatalf("spawned in %q, want the worktree /wt/feature-x", o.spawnDir)
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
