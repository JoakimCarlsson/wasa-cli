package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/joakimcarlsson/wasa/internal/backend"
	"github.com/joakimcarlsson/wasa/internal/finish"
	"github.com/joakimcarlsson/wasa/internal/registry"
	"github.com/joakimcarlsson/wasa/internal/worktree"
)

func init() {
	commands = append(commands, &Command{
		Name:    "finish",
		Summary: "tear down a session: remove its worktree and delete its branch",
		Run:     runFinish,
	})
}

const finishUsage = "usage: wasa finish [--force] <session>"

const finishHelp = `usage: wasa finish [--force] <session>

Tear down a session: stop its tmux session if it is still running, remove its
git worktree and delete its branch. <session> is a session id or title (a unique
id prefix also works); see "wasa session list".

wasa NEVER merges. finish performs no merge, rebase, push or pull request — it
removes local artifacts only. The session's branch is force-deleted, so any
commits on it that you did not merge or push beforehand are discarded for good.
If you want to keep the work, merge or push it yourself before running finish.

Flags:
  --force      remove the worktree even if it has uncommitted or untracked
               changes (discards them); without it, a dirty worktree blocks
               teardown and is reported so you can decide what to do
  -h, --help   show this help and exit
`

func runFinish(args []string) error {
	fs := newFlagSet("wasa finish")
	var force bool
	fs.BoolVar(
		&force,
		"force",
		false,
		"remove the worktree even if it is dirty (discards changes)",
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

	ws, ok := reg.Workspace(s.WorkspaceID)
	if !ok {
		return fmt.Errorf("session %s has no workspace in the registry", s.ID)
	}

	ops := newFinishOps(ws)
	res, err := finish.Session(ops, s, force)
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

	if !reg.RemoveSession(s.ID) {
		return fmt.Errorf("session %s vanished from the registry", s.ID)
	}
	if err := reg.Save(); err != nil {
		return err
	}

	printFinish(s, res)
	return nil
}

// newFinishOps builds the concrete teardown operations bound to ws's repository:
// the default session backend and a worktree Manager rooted at the workspace's
// repo so worktree removal and branch deletion run against the right git
// repository regardless of the working directory.
func newFinishOps(ws *registry.Workspace) finish.Ops {
	return finishOps{
		tmux: backend.Default(),
		wt:   worktree.New(ws.RepoPath, wasaHome(), ws.ID),
	}
}

type finishOps struct {
	tmux backend.SessionBackend
	wt   *worktree.Manager
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
	if res.DeletedBranch {
		fmt.Fprintf(
			os.Stdout,
			"  DISCARDED branch %s — any unmerged work on it is gone for good\n",
			res.Branch,
		)
	}
	fmt.Fprintln(
		os.Stdout,
		"wasa never merges: finish removed local artifacts only.",
	)
}
