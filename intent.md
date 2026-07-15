## Why / Context

When you confirm deleting a session or a workspace in the cockpit, the teardown runs asynchronously with no visible feedback. The confirm modal closes, the UI drops back to a normal-looking session list, and nothing signals that work is in flight. For a workspace delete that cascades across several sessions — each stopping tmux, removing a worktree, and deleting a branch (all shelling out to git/tmux) — this can take a noticeable moment, during which the user has no idea whether their keypress registered, whether it's working, or whether it hung. They may hit the key again or assume it failed.

Session *creation* already handles this: it sets `m.status = "creating session…"` before dispatching (`internal/tui/app.go:630`). Delete and workspace-delete should give the same in-progress feedback.

## What needs to happen

Show an in-progress status ("deleting session…" / "deleting workspace \"<name>\"…") from the moment a delete is confirmed until the result message arrives, so the TUI visibly reflects that a teardown is running. Kill and pause confirmations run the same async-command pattern and should get the same treatment while we're here.

## How it should work

The confirm→command flow lives in `internal/tui/app.go`:

- `enterConfirm` (`internal/tui/app.go:1022`) stores the on-accept command in `m.confirmCmd`.
- On `modal.ConfirmAcceptedMsg` (`internal/tui/app.go:444`) the model sets `m.mode = modeList`, clears `m.confirmCmd`, and returns that command. The command runs asynchronously (Bubble Tea goroutine) and only later produces `deletedMsg` / `workspaceDeletedMsg`, which is the first point `m.status` is updated (`internal/tui/app.go:337`, `:367`).

Between accept and result, `m.status` is untouched — no feedback. The fix is to set a pending status at accept time and let the existing result handlers overwrite it:

1. Add a `confirmPending string` field to the model, set alongside `confirmCmd`. Give `enterConfirm` an extra parameter (or a small paired struct) so each confirm supplies its own pending label. `enterConfirmDelete` (`:906`) passes `"deleting session…"`; `enterWorkspaceDelete` (`:762`) passes `fmt.Sprintf("deleting workspace %q…", ws.Name)`; `enterConfirmKill` / `enterConfirmPause` pass their equivalents.
2. In the `ConfirmAcceptedMsg` case, set `m.status = m.confirmPending` (when non-empty) before returning the command, then clear `confirmPending`.
3. The existing `deletedMsg` / `workspaceDeletedMsg` / `killedMsg` / `pausedMsg` handlers already set a terminal status ("deleted session", etc.) or `m.err` on failure, so the pending status is naturally replaced when the op finishes. No change needed there beyond confirming the pending text is cleared.

This mirrors the "creating session…" precedent exactly and keeps the mechanism generic across all confirm-backed async actions, rather than special-casing delete.

Optional enhancement (nice-to-have, not required for this issue): animate the pending status with a spinner so a long cascade reads as active rather than frozen. The status line is rendered every frame via `statusLine()` (`internal/tui/sessionlist.go:421`); a spinner would need a periodic tick to advance while an op is pending. If added, keep it to the pending window only. A plain "…"-suffixed status already resolves the core problem; the spinner is polish. (Note: TUI pickers/editors are kept bespoke per project convention — if a spinner is added, prefer a minimal bespoke one or `bubbles/spinner` per maintainer preference; call it out in the PR.)

## Acceptance criteria

- [ ] Confirming a session delete immediately shows an in-progress status (e.g. "deleting session…") that remains until the operation completes.
- [ ] Confirming a workspace delete immediately shows an in-progress status naming the workspace (e.g. `deleting workspace "api"…`) until completion.
- [ ] On success the status is replaced by the existing terminal message ("deleted session" / "deleted workspace <name>").
- [ ] On failure the pending status is cleared and the error is surfaced as today (`m.err`), not left showing "deleting…".
- [ ] Cancelling the confirm modal leaves no pending status and no armed command (unchanged from today).
- [ ] The mechanism is generic: kill and pause confirmations show equivalent in-progress status.

## How to verify (runnable)

Unit level, from `wasa-cli/`:

```
make test
```

Add a test in `internal/tui` that arms `enterConfirmDelete` / `enterWorkspaceDelete`, feeds a `modal.ConfirmAcceptedMsg`, and asserts `m.status` is the pending label before any result message; then feeds `deletedMsg{}` / `workspaceDeletedMsg{name: ...}` and asserts the terminal status.

End-to-end (per CLAUDE.local.md, wasa runs under `wsl -d Ubuntu`):

1. Create a workspace with 2–3 sessions so its delete cascade takes a visible moment.
2. Delete the workspace via the confirm modal.
3. Expect: immediately after confirming, the status line reads `deleting workspace "<name>"…`, then flips to `deleted workspace <name>` once the cascade finishes — no window where the UI looks idle with nothing happening.

## Dependencies / notes

Touches `internal/tui/app.go` (`enterConfirm` at `:1022`, `ConfirmAcceptedMsg` at `:444`, `enterConfirmDelete` at `:906`, `enterWorkspaceDelete` at `:762`, result handlers at `:337`/`:367`) and possibly `internal/tui/sessionlist.go` (`statusLine` at `:421`) if the optional spinner is added. Independent of the two teardown correctness bugs (already-removed worktree; already-gone branch) — this is purely cockpit feedback and can land before or after them.