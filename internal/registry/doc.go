// Package registry is wasa's persistent, repo-keyed data model. It stores
// workspaces (a per-repository scope) and sessions (one running agent in one
// worktree on one branch) as a single JSON document under $WASA_HOME, keyed by a
// content-addressed workspace identifier so that re-running inside the same
// repository always resolves to the same workspace. It reconciles persisted
// sessions against tmux on startup and enumerates workspaces most-recently-used
// first.
package registry
