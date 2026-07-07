package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/joakimcarlsson/wasa-cli/internal/backend"
	"github.com/joakimcarlsson/wasa-cli/internal/finish"
	"github.com/joakimcarlsson/wasa-cli/internal/record"
	"github.com/joakimcarlsson/wasa-cli/internal/registry"
	"github.com/joakimcarlsson/wasa-cli/internal/worktree"
)

func init() {
	commands = append(commands, &Command{
		Name:    "finish",
		Summary: "tear down a session: remove its worktree and delete its branch",
		Run:     runFinish,
	})
}

const finishUsage = "usage: wasa finish [--force] [--discard] <session>"

const finishHelp = `usage: wasa finish [--force] [--discard] <session>

Finish a session: stop its tmux session if it is still running and remove its
git worktree. By default the branch is KEPT and the session is marked exited, so
you can pick the work back up later with "wasa session resume <session>".
<session> is a session id or title (a unique id prefix also works); see
"wasa session list".

wasa NEVER merges. finish performs no merge, rebase, push or pull request — it
removes local artifacts only.

Before the worktree is removed, a closing checkpoint of the session (intent,
redacted transcript, commit list) is recorded to refs/wasa/checkpoints; see
"wasa checkpoints". Recording is best-effort and never blocks the teardown.

Flags:
  --force      remove the worktree even if it has uncommitted or untracked
               changes (discards them); without it, a dirty worktree blocks
               teardown and is reported so you can decide what to do
  --discard    also delete the branch and drop the session record entirely, the
               way finish used to behave. Any commits on the branch you did not
               merge or push beforehand are discarded for good, and the session
               can no longer be resumed
  -h, --help   show this help and exit
`

func runFinish(args []string) error {
	fs := newFlagSet("wasa finish")
	var force, discard bool
	fs.BoolVar(
		&force,
		"force",
		false,
		"remove the worktree even if it is dirty (discards changes)",
	)
	fs.BoolVar(
		&discard,
		"discard",
		false,
		"also delete the branch and drop the record (old finish behavior)",
	)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, finishHelp)
			return nil
		}
		return err
	}

	rest := fs.Args()
	if len(rest) != 1 {
		return errors.New(finishUsage)
	}

	reg, _, err := openRegistry()
	if err != nil {
		return err
	}

	s, err := resolveSession(reg, rest[0])
	if err != nil {
		return err
	}

	var ws *registry.Workspace
	if s.WorkspaceID != "" {
		w, ok := reg.Workspace(s.WorkspaceID)
		if !ok {
			return fmt.Errorf(
				"session %s has no workspace in the registry",
				s.ID,
			)
		}
		ws = w
	}
	if ws == nil && (s.WorktreePath != "" || s.Branch != "") {
		return fmt.Errorf(
			"session %s has a worktree or branch but no workspace to remove it against",
			s.ID,
		)
	}

	ops := newFinishOps(ws)

	teardown := finish.Pause
	if discard {
		teardown = finish.Session
	}
	res, err := teardown(ops, s, force)
	if err != nil {
		if !res.RemovedWorktree && !force {
			return fmt.Errorf(
				"%w\nthe worktree may have uncommitted changes; "+
					"re-run with --force to remove it and discard them",
				err,
			)
		}
		return err
	}

	if discard {
		if !reg.RemoveSession(s.ID) {
			return fmt.Errorf("session %s vanished from the registry", s.ID)
		}
	} else {
		reg.MarkExited(s.ID)
		s.WorktreePath = ""
	}
	if err := reg.Save(); err != nil {
		return err
	}

	printFinish(s, res)
	return nil
}

// newFinishOps builds the concrete teardown operations. The session backend is
// always present so tmux is stopped; the worktree Manager is rooted at ws's
// repository so worktree removal and branch deletion run against the right git
// repository regardless of the working directory. ws is nil for a plain session
// launched outside any repository — it has neither worktree nor branch, so no
// worktree Manager is built and finish.Session stops only its tmux.
func newFinishOps(ws *registry.Workspace) finish.Ops {
	o := finishOps{tmux: backend.Default(), home: wasaHome()}
	if ws != nil {
		o.wt = worktree.New(ws.RepoPath, wasaHome(), ws.ID)
		o.repoPath = ws.RepoPath
	}
	return o
}

type finishOps struct {
	tmux     backend.SessionBackend
	wt       *worktree.Manager
	home     string
	repoPath string
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

func (o finishOps) RecordCheckpoint(s *registry.Session) {
	record.FinishSession(o.home, o.repoPath, s)
}

// resolveSession finds the session referenced by query, which is matched against
// each session's id (exact, then unique prefix) and title (exact). It errors
// when nothing matches and when the query is ambiguous, rather than guessing.
func resolveSession(
	reg *registry.Registry,
	query string,
) (*registry.Session, error) {
	if query == "" {
		return nil, errors.New("a session id or title is required")
	}
	if s, ok := reg.Session(query); ok {
		return s, nil
	}

	var matches []*registry.Session
	for _, s := range reg.ListSessions() {
		if s.Title == query || strings.HasPrefix(s.ID, query) {
			matches = append(matches, s)
		}
	}

	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no session matches %q", query)
	case 1:
		return matches[0], nil
	default:
		return nil, fmt.Errorf(
			"%q is ambiguous: %d sessions match; use a full session id",
			query,
			len(matches),
		)
	}
}

func printFinish(s *registry.Session, res finish.Result) {
	label := s.ID
	if s.Title != "" {
		label = fmt.Sprintf("%s (%s)", s.ID, s.Title)
	}
	fmt.Fprintf(os.Stdout, "finished session %s\n", label)

	if res.KilledTmux {
		fmt.Fprintf(os.Stdout, "  killed tmux session %s\n", s.TmuxName)
	}
	if res.RemovedWorktree {
		fmt.Fprintf(os.Stdout, "  removed worktree %s\n", res.WorktreePath)
	}
	switch {
	case res.DeletedBranch:
		fmt.Fprintf(
			os.Stdout,
			"  DISCARDED branch %s — any unmerged work on it is gone for good\n",
			res.Branch,
		)
	case res.Branch != "":
		fmt.Fprintf(
			os.Stdout,
			"  kept branch %s — resume with \"wasa session resume %s\"\n",
			res.Branch, s.ID,
		)
	}
	fmt.Fprintln(
		os.Stdout,
		"wasa never merges: finish removed local artifacts only.",
	)
}
