package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/joakimcarlsson/wasa-cli/internal/backend"
	"github.com/joakimcarlsson/wasa-cli/internal/launch"
	"github.com/joakimcarlsson/wasa-cli/internal/record"
	"github.com/joakimcarlsson/wasa-cli/internal/registry"
	"github.com/joakimcarlsson/wasa-cli/internal/worktree"
)

const sessionResumeHelp = `usage: wasa session resume <session-id|branch>

Resume a finished or paused session: recreate its worktree on the recorded
branch, relaunch its agent and continue from the recorded context rather than
starting cold. <session-id|branch> is a session id or title (a unique id prefix
also works), or the branch a recorded session worked on; the newest session on
that branch is chosen.

The agent continues natively when it supports resuming a prior session (its
transcript is reused, restored from the checkpoint if the local copy is gone);
otherwise it is launched with a compact preamble derived from the checkpoint
(intent, commits produced, recent conversation), and the output says which
happened.

The resumed session is a normal wasa session — registered, visible in the
cockpit and recorded again, with resumedFrom set to the original session id. The
recorded branch must still exist; a deleted branch is a clear error and nothing
is created.
`

const sessionPauseHelp = `usage: wasa session pause [--force] <session>

Pause a session: stop its tmux and remove its worktree, but keep its branch and
registry record so "wasa session resume" can rebuild it. <session> is a session
id or title (a unique id prefix also works).

Flags:
  --force      pause even if the worktree has uncommitted or untracked changes
               (discards them); without it a dirty worktree blocks the pause
  -h, --help   show this help and exit
`

func sessionResume(args []string) error {
	fs := newFlagSet("wasa session resume")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, sessionResumeHelp)
			return nil
		}
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return errors.New("usage: wasa session resume <session-id|branch>")
	}

	reg, _, err := openRegistry()
	if err != nil {
		return err
	}

	src, err := resolveResumeTarget(reg, rest[0])
	if err != nil {
		return err
	}
	if src.Status == registry.StatusRunning {
		return fmt.Errorf(
			"session %s is running; attach to it instead of resuming", src.ID,
		)
	}
	if src.Branch == "" {
		return fmt.Errorf("session %s has no branch to resume", src.ID)
	}
	if src.WorkspaceID == "" {
		return fmt.Errorf(
			"session %s has no workspace to resume against", src.ID,
		)
	}
	ws, ok := reg.Workspace(src.WorkspaceID)
	if !ok {
		return fmt.Errorf("workspace %s not found", src.WorkspaceID)
	}

	wt := worktree.New(ws.RepoPath, wasaHome(), ws.ID)
	if !wt.BranchExists(src.Branch) {
		return fmt.Errorf(
			"branch %s is gone; resume does not recreate branches from "+
				"checkpoint data", src.Branch,
		)
	}
	worktreePath := wt.Path(src.Branch)

	entry, intent, transcript, _ := record.Find(ws.RepoPath, src.ID)

	params := launch.Params{
		Branch:      src.Branch,
		Title:       src.Title,
		Program:     src.Program,
		Profile:     src.ProfileName,
		ResumedFrom: src.ID,
	}
	if native := planResume(
		src.Program, entry.Meta.AgentSessionID, worktreePath, transcript,
	); native != nil {
		params.ResumeArgs = native
		fmt.Fprintf(
			os.Stdout, "resuming %s natively (agent session %s)\n",
			agentLabel(src.Program), entry.Meta.AgentSessionID,
		)
	} else {
		params.InitialPrompt = record.BuildPreamble(intent, entry.Meta, transcript)
		fmt.Fprintln(
			os.Stdout,
			"resuming from checkpoint record, agent-native session not found",
		)
	}

	s, err := launch.CreateSession(wasaHome(), reg, ws, params)
	if err != nil {
		return err
	}
	if err := reg.Save(); err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "resumed session %s from %s\n", s.ID, src.ID)
	fmt.Fprintln(os.Stdout, s.TmuxName)
	return nil
}

// planResume decides how a resumed session continues: it returns the agent's
// native resume argv when the agent supports it and its transcript is available
// (present locally, or restorable from the checkpoint), or nil to fall back to a
// checkpoint-derived preamble.
func planResume(
	program, agentSessionID, worktreePath string, transcript []byte,
) []string {
	args, ok := record.ResumeArgs(program, agentSessionID)
	if !ok {
		return nil
	}
	if record.LocalTranscript(program, agentSessionID, worktreePath) != "" {
		return args
	}
	if len(transcript) > 0 && record.RestoreTranscript(
		program, agentSessionID, worktreePath, transcript,
	) == nil {
		return args
	}
	return nil
}

// resolveResumeTarget finds the session to resume from a query that is either a
// session id/title (via resolveSession) or a branch name, in which case the
// newest session recorded on that branch is chosen. It returns resolveSession's
// error when neither matches, so an ambiguous id prefix is reported as such.
func resolveResumeTarget(
	reg *registry.Registry,
	query string,
) (*registry.Session, error) {
	s, err := resolveSession(reg, query)
	if err == nil {
		return s, nil
	}
	var match *registry.Session
	for _, sess := range reg.ListSessions() {
		if sess.Branch == query &&
			(match == nil || sess.CreatedAt.After(match.CreatedAt)) {
			match = sess
		}
	}
	if match != nil {
		return match, nil
	}
	return nil, err
}

func agentLabel(program string) string {
	if tool, ok := record.AgentForProgram(program); ok {
		return tool
	}
	return program
}

func sessionPause(args []string) error {
	fs := newFlagSet("wasa session pause")
	var force bool
	fs.BoolVar(
		&force,
		"force",
		false,
		"pause even if the worktree is dirty (discards uncommitted changes)",
	)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, sessionPauseHelp)
			return nil
		}
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return errors.New("usage: wasa session pause [--force] <session>")
	}

	reg, _, err := openRegistry()
	if err != nil {
		return err
	}
	s, err := resolveSession(reg, rest[0])
	if err != nil {
		return err
	}
	if err := launch.PauseSession(
		reg, backend.Default(), wasaHome(), s, force,
	); err != nil {
		return err
	}
	if err := reg.Save(); err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "paused session %s\n", s.ID)
	return nil
}
