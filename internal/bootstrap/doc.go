// Package bootstrap materializes a profile's declarative worktree bootstrap
// into a freshly created worktree. A git worktree contains only tracked files,
// so everything untracked a session needs to actually run — dependency trees
// like node_modules/.venv/target, local config like .env/.npmrc — is missing.
// Apply brings the chosen paths across (symlinking the large regenerable ones,
// copying the editable ones) and FreePort hands a session an isolated dev port,
// so the common cases need no hand-written hook.
package bootstrap
