// Package hook runs a profile's post-worktree hook: a shell command executed
// once, immediately after a worktree is created for a session, with its working
// directory set to that worktree and the same environment the session's agent
// will see. It is the seam that turns a freshly created but bare worktree into
// one that can build and run — installing dependencies, materializing a .env,
// warming caches.
//
// Execution is split behind a Runner interface so the command-building and
// failure-surfacing logic is unit-testable without spawning a process. A
// non-zero exit is reported as an *Error carrying the exit code and the tail of
// the hook's captured output. A worktree whose hook failed is deliberately left
// on disk rather than rolled back, so the failure can be diagnosed against the
// real tree; callers must treat the session as not launchable.
package hook
