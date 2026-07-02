// Package launch is wasa's session create/kill orchestration seam. It drives the
// full create flow in one place so the CLI and the TUI invoke the same path
// rather than each reimplementing it. A session is fundamentally a (program,
// working directory) pair and comes in two shapes:
//
//   - A worktree session resolves the profile environment, adds a branch +
//     worktree under $WASA_HOME, runs the post-worktree hook, and spawns the
//     program in the worktree. It is reached only when a Branch is supplied.
//   - A plain session spawns the program directly in a working directory with no
//     branch and no worktree, and therefore runs no post-worktree hook. If a
//     workspace is present it still resolves that workspace's profile
//     environment; with no workspace it runs with no profile and an empty
//     environment, so an agent can be launched anywhere.
//
// Killing a session stops its tmux and marks it exited; worktree teardown is the
// separate finish lifecycle and is not done here.
package launch
