// Package launch is wasa's session create/kill orchestration seam. It drives the
// full create flow in one place — resolve the profile environment, add the
// worktree, run the post-worktree hook, spawn the tmux session with that
// environment, and register the session — so the CLI and the TUI invoke the same
// path rather than each reimplementing it. Killing a session stops its tmux and
// marks it exited; worktree teardown is the separate finish lifecycle and is not
// done here.
package launch

import (
	"github.com/joakimcarlsson/wasa/internal/hook"
	"github.com/joakimcarlsson/wasa/internal/profile"
	"github.com/joakimcarlsson/wasa/internal/registry"
	"github.com/joakimcarlsson/wasa/internal/tmux"
	"github.com/joakimcarlsson/wasa/internal/worktree"
)

// DefaultProgram is the program a session runs when the caller names none. wasa
// is a cockpit for AI coding agents, so the default is the claude agent rather
// than a bare shell.
const DefaultProgram = "claude"

// Params describes a session to create. Branch is required; an empty Program
// defaults to DefaultProgram and an empty Profile selects the workspace default.
type Params struct {
	Branch  string
	Title   string
	Program string
	Profile string
}

// CreateSession runs the full create flow for ws and registers the resulting
// session in reg, returning it. It does not Save reg; the caller persists once
// the in-memory mutation is acceptable. home is the resolved $WASA_HOME used to
// place the worktree. A failure at any step returns before the session is
// registered, except that a post-worktree hook failure deliberately leaves the
// worktree on disk for inspection.
func CreateSession(
	home string,
	reg *registry.Registry,
	ws *registry.Workspace,
	p Params,
) (*registry.Session, error) {
	program := p.Program
	if program == "" {
		program = DefaultProgram
	}

	prof, err := ws.SelectProfile(p.Profile)
	if err != nil {
		return nil, err
	}

	env, err := profile.Resolve(prof, program)
	if err != nil {
		return nil, err
	}

	worktreePath, err := worktree.New(ws.RepoPath, home, ws.ID).Add(p.Branch)
	if err != nil {
		return nil, err
	}

	sessionID := registry.NewSessionID()
	if err := hook.Run(hook.ShellRunner{}, hook.Hook{
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
	if err := tmux.New().SpawnEnv(tmuxName, worktreePath, env, program); err != nil {
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

// KillSession kills the session's tmux session and marks it exited in reg. It
// does not Save reg and does not remove the worktree, which is the separate
// finish lifecycle. A tmux failure is returned without changing the recorded
// status.
func KillSession(reg *registry.Registry, s *registry.Session) error {
	if err := tmux.New().Kill(s.TmuxName); err != nil {
		return err
	}
	reg.MarkExited(s.ID)
	return nil
}
