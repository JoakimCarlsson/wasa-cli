## Why / Context

Creating a new session whose branch name matches a session that already exists dies with a raw, low-level git error instead of being handled gracefully. The worktree for that branch is still on disk from the earlier session, so `git worktree add` refuses to reuse the path:

```
git worktree add /home/jcarlsson/.wasa/worktrees/0c1bb6a9d11f/task-141-upgrade-the-tui-to-bubble-tea-v2 task/141
fatal: '/home/jcarlsson/.wasa/worktrees/0c1bb6a9d11f/task-141-upgrade-the-tui-to-bubble-tea-v2' already exists
```

This is a common flow — re-running a task after an earlier session for the same branch wasn't torn down (or was left behind by a crash). The user hits a dead end with a git internals message and no way forward inside wasa; they have to drop to a shell, figure out the worktree path, and `git worktree remove` it by hand. A runner that owns worktree lifecycle should recognize the collision and offer to clean up the stale one.

## What needs to happen

Detect the "branch / worktree already exists" collision at session-creation time and turn the raw `fatal:` into a handled path that offers to clear the old worktree (and the associated session state) before retrying, rather than letting git's error bubble straight up.

The relevant code:

- `internal/worktree/worktree.go` → `Manager.Add(branch)` runs `git worktree add` and returns git's error verbatim (`worktree.go:125`). `Manager.Remove(target, force)` already exists and already handles the "worktree dir already gone → prune stale metadata" case, so the teardown primitive is available.
- `internal/launch/launch.go` → `defaultOps().addWorktree` (`launch.go:89`) calls `m.Add(branch)` and returns the error up the launch path with no special handling.
- `internal/cli/worktree.go` → `worktreeAdd` (`worktree.go:55`) surfaces the same error on the CLI path.

## How it should work

- Recognize the collision explicitly rather than string-matching deep in the stack: before/at `Add`, check whether a worktree already exists for the computed path (`Manager.Path(branch)` + `List()`), or classify git's `already exists` / `already checked out` failure into a typed sentinel error (e.g. `ErrWorktreeExists`) so callers can branch on it.
- When the collision is detected, the session-creation flow (both the TUI new-session path and, where interactive, the CLI) should **ask** the user what to do instead of aborting:
  - **Reuse / clear the old worktree** — tear down the stale worktree via `Manager.Remove(path, force=true)` (plus prune) and any lingering session/tmux state for that branch, then retry `Add`.
  - **Cancel** — abort cleanly with a clear message naming the existing session/worktree, not a `fatal:` dump.
- Clearing must also reconcile session state and tmux: if a session record still points at that branch/worktree, don't leave an orphan. Reuse the existing teardown that a normal session removal runs so state, worktree, and tmux window all go together.
- Non-interactive contexts (plain `wasa worktree add`, scripts) can't prompt — there they should fail with a clear, actionable message (name the existing worktree path and suggest the remove command / a `--force` style flag), not the raw git error. Decide whether `wasa worktree add` grows a `--force`/`--replace` flag to opt into clearing non-interactively.
- Guard against clobbering a live session: if the colliding worktree belongs to a session that is currently running (tmux window alive), warn plainly and require explicit confirmation before removing it.

## Acceptance criteria

- [ ] Creating a session for a branch whose worktree already exists no longer surfaces the raw `fatal: '...' already exists` git message.
- [ ] The interactive (TUI) new-session flow detects the collision and prompts the user to either clear the existing worktree and retry, or cancel.
- [ ] Choosing "clear" removes the stale worktree (and reconciles its session/tmux state) and then successfully creates the new session on that branch.
- [ ] Choosing "cancel" aborts with a clear message naming the existing session/worktree and leaves the existing worktree untouched.
- [ ] Non-interactive `wasa worktree add <branch>` against an existing worktree fails with a clear, actionable message (not the raw git `fatal:`), and there is a defined way to force replacement.
- [ ] A collision against a *currently running* session requires explicit confirmation before its worktree is removed.

## How to verify (runnable)

Run inside `wsl -d Ubuntu` against a real repo (wasa is Linux/tmux only). Reproduce the collision first, then confirm the handled flow.

1. Reproduce the raw error (pre-fix baseline):
   ```
   wasa worktree add some-branch      # first time: succeeds, prints the worktree path
   wasa worktree add some-branch      # second time: today prints `fatal: '...' already exists`
   ```
2. TUI path: start wasa, create a session on `some-branch`, tear nothing down, then create another session on `some-branch`. Expect a prompt offering **clear & retry** / **cancel** — not a crash or raw git error.
3. Choose **clear & retry** → expect a working session on `some-branch` with a fresh worktree, and `git worktree list` in the repo showing exactly one worktree for that branch.
4. Choose **cancel** → expect a clear message and the original worktree/session still intact (`git worktree list` unchanged).
5. Non-interactive: `wasa worktree add some-branch` on an existing worktree exits non-zero with an actionable message; the documented force path replaces it and exits zero.
6. `make test` and `make lint` pass.

Per the repo's definition of done, also validate end-to-end by driving a real agent session through this flow (create → collide → clear → session runs) in the afterdark repo.

## Dependencies / notes

Teardown primitive already exists: `Manager.Remove` in `internal/worktree/worktree.go` handles force-removal and pruning stale metadata. The fix is primarily (a) classifying the collision and (b) wiring the prompt + reconciled cleanup into the launch/TUI path.