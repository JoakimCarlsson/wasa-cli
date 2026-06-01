// Package launch is wasa's session create/kill orchestration seam. It drives the
// full create flow in one place so the CLI and the TUI invoke the same path
// rather than each reimplementing it. A session is fundamentally a (program,
// working directory) pair and comes in two shapes:
//
//   - A worktree session resolves the profile environment, adds a branch +
//     worktree under $WASA_HOME, runs the post-worktree hook, and spawns the
//     program in the worktree. It is reached only when a Branch is supplied.
//   - A plain session spawns the program directly in a working directory with no
//     branch and no worktree, and therefore runs no post-worktree hook. If a
//     workspace is present it still resolves that workspace's profile
//     environment; with no workspace it runs with no profile and an empty
//     environment, so an agent can be launched anywhere.
//
// Killing a session stops its tmux and marks it exited; worktree teardown is the
// separate finish lifecycle and is not done here.
package launch

import (
	"errors"

	"github.com/joakimcarlsson/wasa/internal/backend"
	"github.com/joakimcarlsson/wasa/internal/hook"
	"github.com/joakimcarlsson/wasa/internal/profile"
	"github.com/joakimcarlsson/wasa/internal/registry"
	"github.com/joakimcarlsson/wasa/internal/worktree"
)

// Params describes a session to create. A non-empty Branch selects a worktree
// session created on that branch; an empty Branch selects a plain session run in
// WorkingDir. An empty Program runs the backend's OS shell and an empty Profile
// selects the workspace default. Callers derive a concrete Program from
// DetectAgents and Shell rather than relying on a hardcoded agent name.
type Params struct {
	Branch     string
	Title      string
	Program    string
	Profile    string
	WorkingDir string
}

// ops are the side-effecting operations the create flow performs, injected so
// the flow can be unit-tested without a real git repository, tmux server or
// hook process. defaultOps binds them to the production worktree, hook and
// backend implementations.
type ops struct {
	addWorktree func(repoPath, home, workspace, branch string) (string, error)
	runHook     func(h hook.Hook) error
	spawn       func(name, dir string, env []string, program string) error
}

func defaultOps() ops {
	return ops{
		addWorktree: func(repoPath, home, workspace, branch string) (string, error) {
			return worktree.New(repoPath, home, workspace).Add(branch)
		},
		runHook: func(h hook.Hook) error {
			return hook.Run(hook.ShellRunner{}, h)
		},
		spawn: func(name, dir string, env []string, program string) error {
			return backend.Default().SpawnEnv(name, dir, env, program)
		},
	}
}

// CreateSession runs the full create flow and registers the resulting session
// in reg, returning it. It does not Save reg; the caller persists once the
// in-memory mutation is acceptable. home is the resolved $WASA_HOME used to
// place a worktree. ws may be nil for a plain session launched outside any
// repository, in which case the session carries no profile environment. A
// failure at any step returns before the session is registered, except that a
// post-worktree hook failure deliberately leaves the worktree on disk for
// inspection.
func CreateSession(
	home string,
	reg *registry.Registry,
	ws *registry.Workspace,
	p Params,
) (*registry.Session, error) {
	return createSession(defaultOps(), home, reg, ws, p)
}

func createSession(
	o ops,
	home string,
	reg *registry.Registry,
	ws *registry.Workspace,
	p Params,
) (*registry.Session, error) {
	program := p.Program

	var (
		prof registry.Profile
		env  []string
	)
	if ws != nil {
		var err error
		prof, err = ws.SelectProfile(p.Profile)
		if err != nil {
			return nil, err
		}
		env, err = profile.Resolve(prof, program)
		if err != nil {
			return nil, err
		}
	}

	if p.Branch != "" {
		return createWorktreeSession(o, home, reg, ws, prof, env, program, p)
	}
	return createPlainSession(o, reg, ws, prof, env, program, p)
}

// createWorktreeSession adds a branch + worktree, runs the post-worktree hook
// and spawns the program in the worktree. A worktree session requires a
// workspace, since the branch and worktree are created against its repository.
func createWorktreeSession(
	o ops,
	home string,
	reg *registry.Registry,
	ws *registry.Workspace,
	prof registry.Profile,
	env []string,
	program string,
	p Params,
) (*registry.Session, error) {
	if ws == nil {
		return nil, errors.New("a worktree session requires a workspace")
	}

	worktreePath, err := o.addWorktree(ws.RepoPath, home, ws.ID, p.Branch)
	if err != nil {
		return nil, err
	}

	sessionID := registry.NewSessionID()
	if err := o.runHook(hook.Hook{
		Command:      prof.PostWorktreeHook,
		RepoPath:     ws.RepoPath,
		WorktreePath: worktreePath,
		Branch:       p.Branch,
		Session:      sessionID,
		Env:          env,
	}); err != nil {
		return nil, err
	}

	tmuxName := registry.TmuxName(ws.ID, sessionID)
	if err := o.spawn(tmuxName, worktreePath, env, program); err != nil {
		return nil, err
	}

	s := &registry.Session{
		ID:           sessionID,
		WorkspaceID:  ws.ID,
		ProfileName:  prof.Name,
		Title:        p.Title,
		Program:      program,
		Branch:       p.Branch,
		WorktreePath: worktreePath,
		TmuxName:     tmuxName,
	}
	reg.AddSession(s)
	return s, nil
}

// createPlainSession spawns the program directly in p.WorkingDir with no branch
// and no worktree, so the post-worktree hook never runs. When a workspace is
// present its profile name and resolved environment are carried; otherwise both
// are empty and WorkspaceID is left blank.
func createPlainSession(
	o ops,
	reg *registry.Registry,
	ws *registry.Workspace,
	prof registry.Profile,
	env []string,
	program string,
	p Params,
) (*registry.Session, error) {
	var workspaceID, profileName string
	if ws != nil {
		workspaceID = ws.ID
		profileName = prof.Name
	}

	sessionID := registry.NewSessionID()
	tmuxName := registry.TmuxName(workspaceID, sessionID)
	if err := o.spawn(tmuxName, p.WorkingDir, env, program); err != nil {
		return nil, err
	}

	s := &registry.Session{
		ID:          sessionID,
		WorkspaceID: workspaceID,
		ProfileName: profileName,
		Title:       p.Title,
		Program:     program,
		WorkingDir:  p.WorkingDir,
		TmuxName:    tmuxName,
	}
	reg.AddSession(s)
	return s, nil
}

// KillSession kills the session's tmux session and marks it exited in reg. It
// does not Save reg and does not remove the worktree, which is the separate
// finish lifecycle. A tmux failure is returned without changing the recorded
// status.
func KillSession(reg *registry.Registry, s *registry.Session) error {
	if err := backend.Default().Kill(s.TmuxName); err != nil {
		return err
	}
	reg.MarkExited(s.ID)
	return nil
}

// DeleteSession removes the session's record from reg entirely, killing its tmux
// session first when it is still running. Unlike KillSession, which keeps the
// exited record, this drops the record so the session no longer appears in the
// cockpit. A tmux kill failure is returned without removing the record, so the
// session is not orphaned from the registry while its tmux is still alive. Like
// KillSession it does not Save reg and does not remove the worktree, which is the
// separate finish lifecycle.
func DeleteSession(reg *registry.Registry, s *registry.Session) error {
	if s.Status == registry.StatusRunning {
		if err := backend.Default().Kill(s.TmuxName); err != nil {
			return err
		}
	}
	reg.RemoveSession(s.ID)
	return nil
}
