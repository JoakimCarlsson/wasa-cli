// Package worktree wraps the git worktree porcelain. It shells out to the git
// binary rather than vendoring a git library, and routes every worktree path
// through a single placement seam (see Layout) so the on-disk layout can change
// without touching callers.
package worktree
