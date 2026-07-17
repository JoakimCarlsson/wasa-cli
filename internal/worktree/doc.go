// Package worktree wraps the git worktree porcelain. It shells out to the git
// binary rather than vendoring a git library, and routes every worktree path
// through a single placement seam (see Layout) so the on-disk layout can change
// without touching callers.
//
// It also owns wasa's isolate policy for project-scoped agent config
// directories (.claude, .cursor, ...): a new worktree carries whatever git
// checks out for its branch and leaves untracked local config behind rather
// than copying it in. ProjectConfigStates makes that policy observable; see
// ProjectConfigState.
package worktree
