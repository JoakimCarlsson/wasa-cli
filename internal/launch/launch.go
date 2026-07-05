package launch

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/joakimcarlsson/wasa-cli/internal/backend"
	"github.com/joakimcarlsson/wasa-cli/internal/bootstrap"
	"github.com/joakimcarlsson/wasa-cli/internal/finish"
	"github.com/joakimcarlsson/wasa-cli/internal/hook"
	"github.com/joakimcarlsson/wasa-cli/internal/profile"
	"github.com/joakimcarlsson/wasa-cli/internal/record"
	"github.com/joakimcarlsson/wasa-cli/internal/registry"
	"github.com/joakimcarlsson/wasa-cli/internal/sessionstatus"
	"github.com/joakimcarlsson/wasa-cli/internal/worktree"
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
	// addWorktree adds the branch's worktree and returns its path along with the
	// repository HEAD it branched from, captured for later diffing.
	addWorktree func(
		repoPath, home, workspace, branch string,
	) (path, baseCommit string, err error)
	// applyPaths materializes the profile's declarative bootstrap into the new
	// worktree: symlinking its LinkPaths and copying its CopyPaths from the
	// repository. A source that does not exist is skipped, not fatal.
	applyPaths func(repoPath, worktreePath string, prof registry.Profile) error
	// allocatePort returns a free local TCP port, used when a profile sets
	// PortEnv so concurrent sessions do not collide on the same dev port.
	allocatePort func() (int, error)
	runHook      func(h hook.Hook) error
	spawn        func(name, dir string, env []string, program string) error
	// prepareHooks augments the spawn environment for a session and, for a
	// hook-emitting agent, installs the lifecycle hook that makes it report
	// status to wasa. It returns the environment the program is spawned with.
	prepareHooks func(home, sessionID, program string, env []string) []string
	// installRecordHooks installs the session-recording hook configuration
	// into the new worktree for a supported agent, so the session's
	// transcript and commits are captured as checkpoints. Best-effort: a
	// failure logs one warning and the session still launches unrecorded.
	installRecordHooks func(worktreePath, program string)
}

func defaultOps() ops {
	return ops{
		addWorktree: func(
			repoPath, home, workspace, branch string,
		) (string, string, error) {
			m := worktree.New(repoPath, home, workspace)
			base, err := m.HeadSHA()
			if err != nil {
				return "", "", err
			}
			path, err := m.Add(branch)
			if err != nil {
				return "", "", err
			}
			return path, base, nil
		},
		applyPaths: func(
			repoPath, worktreePath string, prof registry.Profile,
		) error {
			skipped, err := bootstrap.Apply(
				repoPath, worktreePath, prof.LinkPaths, prof.CopyPaths,
			)
			for _, rel := range skipped {
				log.Printf(
					"wasa: worktree bootstrap skipped missing path %q", rel,
				)
			}
			return err
		},
		allocatePort: bootstrap.FreePort,
		runHook: func(h hook.Hook) error {
			return hook.Run(hook.ShellRunner{}, h)
		},
		spawn: func(name, dir string, env []string, program string) error {
			return backend.Default().SpawnEnv(name, dir, env, program)
		},
		prepareHooks:       prepareHooks,
		installRecordHooks: installRecordHooks,
	}
}

// installRecordHooks writes wasa's recording hooks into the worktree's agent
// configuration (e.g. Claude Code's .claude/settings.json, Gemini's
// .gemini/settings.json) so the session reports transcript and commit events
// to `wasa record-hook`. The configuration lives in the worktree and
// disappears with it at finish. An agent with no recording integration
// launches unrecorded.
func installRecordHooks(worktreePath, program string) {
	tool, ok := record.AgentForProgram(program)
	if !ok {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if err := record.InstallHooks(worktreePath, tool, exe); err != nil {
		log.Printf("wasa: session recording hooks not installed: %v", err)
	}
}

// prepareHooks adds the WASA_SESSION and WASA_HOME variables every session needs
// for `wasa hook-handler` to identify itself, then — for a hook-emitting agent —
// installs wasa's lifecycle hook into that agent's configuration so it reports
// status to the cockpit. Installation is best-effort: any failure is swallowed
// and the session still launches, falling back to the pane heuristic. The
// returned slice is the environment the program is spawned with.
func prepareHooks(home, sessionID, program string, env []string) []string {
	env = append(env,
		hook.EnvSession+"="+sessionID,
		"WASA_HOME="+home,
	)
	adapter, ok := sessionstatus.For(program)
	if !ok {
		return env
	}
	exe, err := os.Executable()
	if err != nil {
		return env
	}
	_ = adapter.Install(env, exe+" hook-handler --tool "+adapter.Name())
	return env
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
	return createPlainSession(o, home, reg, ws, prof, env, program, p)
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

	worktreePath, baseCommit, err := o.addWorktree(
		ws.RepoPath, home, ws.ID, p.Branch,
	)
	if err != nil {
		return nil, err
	}

	if err := o.applyPaths(ws.RepoPath, worktreePath, prof); err != nil {
		return nil, err
	}

	if prof.PortEnv != "" {
		port, err := o.allocatePort()
		if err != nil {
			return nil, err
		}
		env = append(env, prof.PortEnv+"="+strconv.Itoa(port))
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

	o.installRecordHooks(worktreePath, program)

	tmuxName := registry.TmuxName(ws.ID, sessionID)
	spawnEnv := o.prepareHooks(home, sessionID, program, env)
	if err := o.spawn(tmuxName, worktreePath, spawnEnv, program); err != nil {
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
		BaseCommit:   baseCommit,
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
	home string,
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
	spawnEnv := o.prepareHooks(home, sessionID, program, env)
	if err := o.spawn(tmuxName, p.WorkingDir, spawnEnv, program); err != nil {
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

// PauseSession soft-stops s: its tmux is killed and its worktree removed, but —
// unlike finish — its branch and its registry record are kept, and the session
// is marked paused so ResumeSession can rebuild it. A plain session simply has
// its tmux stopped. force discards uncommitted worktree changes; when false a
// dirty worktree blocks the pause with git's error and the session stays
// running. It does not Save reg; the caller persists.
func PauseSession(
	reg *registry.Registry,
	be backend.SessionBackend,
	home string,
	s *registry.Session,
	force bool,
) error {
	ws, ok := reg.Workspace(s.WorkspaceID)
	if s.WorktreePath != "" && !ok {
		return fmt.Errorf("workspace %s not found", s.WorkspaceID)
	}
	var wt *worktree.Manager
	if ok {
		wt = worktree.New(ws.RepoPath, home, ws.ID)
	}

	ops := finishOps{tmux: be, wt: wt, home: home}
	if _, err := finish.Pause(ops, s, force); err != nil {
		return err
	}
	reg.MarkPaused(s.ID)
	return nil
}

// ResumeSession rebuilds a paused session and spawns it again: the worktree is
// re-attached to the saved branch (which still exists, so no branch is
// created), the profile bootstrap, environment and post-worktree hook are
// re-applied, and the program is re-spawned under the session's original tmux
// name. A profile port is re-allocated rather than reused, since the old port
// may have been taken while the session was paused. The session's BaseCommit is
// deliberately left untouched, so the diff after a resume is still measured
// from the original session start, not the resume point. A plain session is
// simply re-spawned in its working directory. It does not Save reg; the caller
// persists.
func ResumeSession(
	home string,
	reg *registry.Registry,
	s *registry.Session,
) error {
	return resumeSession(defaultOps(), home, reg, s)
}

func resumeSession(
	o ops,
	home string,
	reg *registry.Registry,
	s *registry.Session,
) error {
	if s.Status == registry.StatusRunning {
		return errors.New("session is already running")
	}

	var (
		ws   *registry.Workspace
		prof registry.Profile
		env  []string
	)
	if s.WorkspaceID != "" {
		var ok bool
		ws, ok = reg.Workspace(s.WorkspaceID)
		if !ok {
			return fmt.Errorf("workspace %s not found", s.WorkspaceID)
		}
		var err error
		prof, err = ws.SelectProfile(s.ProfileName)
		if err != nil {
			return err
		}
		env, err = profile.Resolve(prof, s.Program)
		if err != nil {
			return err
		}
	}

	dir := s.WorkingDir
	if s.Branch != "" {
		if ws == nil {
			return errors.New("a worktree session requires a workspace")
		}
		worktreePath, _, err := o.addWorktree(
			ws.RepoPath, home, ws.ID, s.Branch,
		)
		if err != nil {
			return err
		}

		if err := o.applyPaths(ws.RepoPath, worktreePath, prof); err != nil {
			return err
		}

		if prof.PortEnv != "" {
			port, err := o.allocatePort()
			if err != nil {
				return err
			}
			env = append(env, prof.PortEnv+"="+strconv.Itoa(port))
		}

		if err := o.runHook(hook.Hook{
			Command:      prof.PostWorktreeHook,
			RepoPath:     ws.RepoPath,
			WorktreePath: worktreePath,
			Branch:       s.Branch,
			Session:      s.ID,
			Env:          env,
		}); err != nil {
			return err
		}

		o.installRecordHooks(worktreePath, s.Program)

		s.WorktreePath = worktreePath
		dir = worktreePath
	}

	spawnEnv := o.prepareHooks(home, s.ID, s.Program, env)
	if err := o.spawn(s.TmuxName, dir, spawnEnv, s.Program); err != nil {
		return err
	}

	s.Status = registry.StatusRunning
	reg.MarkAttached(s.ID)
	return nil
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

// DeleteWorkspace tears down every session owned by ws and then removes the
// workspace from reg. Each session is finished with force — its tmux stopped, its
// worktree removed and its branch deleted, discarding any uncommitted changes —
// its status file removed and its record dropped; the workspace record is removed
// last so it never lingers without its sessions. force is used because a bulk
// delete cannot stop to ask about a single dirty worktree; the caller is expected
// to have warned the user that uncommitted work is discarded. reg is not saved;
// the caller saves. It returns the number of sessions torn down. A teardown error
// stops the cascade and is returned with the count already removed, leaving the
// remaining sessions and the workspace in place so the caller can retry.
func DeleteWorkspace(
	reg *registry.Registry,
	be backend.SessionBackend,
	home string,
	ws *registry.Workspace,
) (int, error) {
	var sessions []*registry.Session
	for _, s := range reg.ListSessions() {
		if s.WorkspaceID == ws.ID {
			sessions = append(sessions, s)
		}
	}

	ops := finishOps{
		tmux: be,
		wt:   worktree.New(ws.RepoPath, home, ws.ID),
		home: home,
	}
	for i, s := range sessions {
		if _, err := finish.Session(ops, s, true); err != nil {
			return i, err
		}
		_ = sessionstatus.Remove(home, s.ID)
		reg.RemoveSession(s.ID)
	}

	reg.RemoveWorkspace(ws.ID)
	return len(sessions), nil
}

// finishOps adapts the session backend and the workspace's worktree manager to
// the finish.Ops teardown interface, so DeleteWorkspace reuses the same tmux →
// worktree → branch sequence as `wasa finish` rather than reimplementing it.
type finishOps struct {
	tmux backend.SessionBackend
	wt   *worktree.Manager
	home string
}

func (o finishOps) TmuxAlive(
	name string,
) (bool, error) {
	return o.tmux.Has(name)
}

func (o finishOps) KillTmux(name string) error { return o.tmux.Kill(name) }

func (o finishOps) RemoveWorktree(path string, force bool) error {
	return o.wt.Remove(path, force)
}

func (o finishOps) DeleteBranch(branch string) error {
	return o.wt.DeleteBranch(branch, true)
}

// RecordCheckpoint writes the closing checkpoint. A nil worktree manager means
// the session ran outside any workspace, with no repository to record against,
// so recording is skipped.
func (o finishOps) RecordCheckpoint(s *registry.Session) {
	if o.wt == nil {
		return
	}
	record.FinishSession(o.home, o.wt.RepoDir, s)
}
