// Package finish is wasa's session teardown lifecycle. It stops a session's
// tmux, removes its git worktree and deletes its branch — and nothing else. It
// deliberately never merges, rebases, pushes or opens a pull request: finish
// removes local artifacts only, leaving every decision about preserving the work
// to the user, who must have merged or pushed it beforehand.
//
// The git and tmux operations are injected through Ops so the teardown sequence
// can be unit-tested without a real repository or tmux server, and so a test can
// assert by construction that no merge-like operation is ever invoked: the Ops
// interface exposes no method that could merge, rebase, push or open a PR.
package finish
