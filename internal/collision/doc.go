// Package collision detects when two or more live worktree sessions in the
// same workspace have changed the same path. It is the single seam both the
// cockpit's per-session indicator and the opt-in launch-time context
// injection read from, so the intersection is computed once and the two
// features can never disagree.
package collision
