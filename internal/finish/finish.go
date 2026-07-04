package finish

import (
	"errors"
	"fmt"

	"github.com/joakimcarlsson/wasa-cli/internal/registry"
)

// Ops are the git and tmux operations Session performs, injected so the teardown
// can be tested in isolation. The interface is intentionally minimal and exposes
// no merge, rebase, push or pull-request operation: finish cannot perform one
// because there is no method to call.
type Ops interface {
	// TmuxAlive reports whether the named tmux session still exists.
	TmuxAlive(tmuxName string) (bool, error)
	// KillTmux stops the named tmux session.
	KillTmux(tmuxName string) error
	// RemoveWorktree removes the git worktree at path. When force is false a
	// dirty worktree blocks the removal and the underlying error is returned;
	// force discards the worktree's uncommitted changes.
	RemoveWorktree(path string, force bool) error
	// DeleteBranch force-deletes the branch, discarding any unmerged work.
	DeleteBranch(branch string) error
	// RecordCheckpoint writes the session's closing checkpoint to the
	// recording ref while the branch and transcript still exist. It is
	// best-effort by contract: an implementation logs at most one warning
	// and returns nothing, so recording can never fail a teardown.
	RecordCheckpoint(s *registry.Session)
}

// Result reports what teardown did, so the caller can describe the outcome and,
// in particular, say loudly that an unmerged branch was discarded.
type Result struct {
	// KilledTmux is true when a still-running tmux session was stopped.
	KilledTmux bool
	// RemovedWorktree is true when the worktree was removed.
	RemovedWorktree bool
	// DeletedBranch is true when the branch was deleted.
	DeletedBranch bool
	// Branch is the deleted branch name, for the caller's output.
	Branch string
	// WorktreePath is the removed worktree path, for the caller's output.
	WorktreePath string
}

// Session tears down s: it stops the session's tmux if it is still alive,
// records the session's closing checkpoint, then removes the worktree, then
// deletes the branch. The order is fixed twice over: recording must run while
// the branch and worktree still exist, and git refuses to delete a branch
// that is checked out in a worktree, so the worktree must go before the
// branch. No step merges, rebases, pushes or opens a pull request.
//
// When force is false a dirty worktree blocks the removal: Session returns that
// error before deleting the branch, leaving the branch and the session intact so
// the caller can re-run with force once the user has dealt with the changes. The
// branch itself is always force-deleted, because wasa never merges and a
// session's branch is therefore routinely unmerged at teardown.
func Session(ops Ops, s *registry.Session, force bool) (Result, error) {
	if s == nil {
		return Result{}, errors.New("session must not be nil")
	}

	res := Result{Branch: s.Branch, WorktreePath: s.WorktreePath}

	if s.TmuxName != "" {
		alive, err := ops.TmuxAlive(s.TmuxName)
		if err != nil {
			return res, fmt.Errorf("probe tmux session: %w", err)
		}
		if alive {
			if err := ops.KillTmux(s.TmuxName); err != nil {
				return res, fmt.Errorf("kill tmux session: %w", err)
			}
			res.KilledTmux = true
		}
	}

	ops.RecordCheckpoint(s)

	if s.WorktreePath != "" {
		if err := ops.RemoveWorktree(s.WorktreePath, force); err != nil {
			return res, fmt.Errorf("remove worktree: %w", err)
		}
		res.RemovedWorktree = true
	}

	if s.Branch != "" {
		if err := ops.DeleteBranch(s.Branch); err != nil {
			return res, fmt.Errorf("delete branch: %w", err)
		}
		res.DeletedBranch = true
	}

	return res, nil
}
