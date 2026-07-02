# AGENTS.md — wasa-cli

The wasa **runner**: a terminal cockpit that launches and supervises AI coding
agents, each in its own git worktree, kept alive in tmux, shown in a Bubble Tea
TUI. Standalone — the control plane is a layer you `wasa link` into, never a
dependency. Workspace map: `../AGENTS.md`; product: `../VISION.md`.

**Stack:** Go 1.26.3 · Bubble Tea + Bubbles + lipgloss (TUI) · tmux (sessions,
shelled out) · git (worktrees, shelled out). Linux/macOS only — the Windows
entry point just prints "use WSL2" and exits.

## Commands

```sh
make install   # install golangci-lint, goimports, golines
make build     # go build -buildvcs=false -o bin/wasa ./cmd/wasa
make run       # build then run
make fmt       # goimports -w . && golines -m 80 -w .
make lint      # go vet ./... && golangci-lint run ./...
make env       # (Windows) re-run inside WSL; put a fresh bin/wasa first on PATH
```

**Verify a change:** get `make lint` clean, then build and drive the affected
flow. Anything touching tmux must be exercised inside WSL2 (a distro with tmux +
Go), not native Windows.

## Layout

`internal/<name>/` dirs are subsystems. Orchestration seams each live in one
place so the CLI and the TUI drive the same path.

**Seams:**

- `launch/` — session create/kill orchestration (worktree → hook → bootstrap → tmux). The one create flow both CLI and TUI use.
- `finish/` — teardown: stop tmux, remove worktree, delete branch. Never merges/rebases/pushes — local artifacts only.
- `backend/` — the session-backend interface + `Default` (host selector). `backend/unix/` is the tmux impl (`//go:build !windows`).
- `worktree/` — git worktree porcelain (shells out to `git`).
- `bootstrap/` — copies/symlinks untracked-but-needed files (deps, `.env`) into a fresh worktree; assigns an isolated dev port.
- `hook/` — runs a profile's post-worktree hook (deps install, `.env`, cache warm).
- `profile/` — resolves a profile into the `KEY=VALUE` env injected at launch.
- `repo/` — resolves a directory to its canonical git identity → the content-addressed workspace id.
- `registry/` — persistent repo-keyed data model (workspaces + sessions) as one JSON doc under `$WASA_HOME`; reconciles against tmux on startup.
- `sessionstatus/` — per-session activity state (working/waiting/idle) and how it's derived.
- `config/` — loads `$WASA_HOME/config.json` over defaults; owns the theme/keys/layout schema; validates at startup.

**Entry & UI:**

- `cmd/wasa/` — `main` (`//go:build !windows`) calls `cli.Run(version, args)`; `main_windows.go` is the WSL stub. `version` via `-ldflags "-X main.version=…"`.
- `cli/` — flag parsing, usage, subcommand dispatch.
- `tui/` — the cockpit (Bubble Tea): one tab per workspace, sessions with status dots, create/attach/kill. Drives the seams; never reimplements them.
- `tui/theme/` — resolved lipgloss styles. A leaf package (config + lipgloss only) so every layer imports `Theme` without an import cycle.
- `tui/component/` — generic building blocks (keymap, pickers, tab strip, overlay helpers). Knows nothing about registry/sessions/workspaces.
- `tui/modal/` — full-screen modals (create form, confirm, settings editor).
- `tui/pane/` — right-pane feature machines (live preview, git diff, companion terminal).

## Hard rules

- **The CLI is standalone** — the web / control-plane is linked in, never a build or runtime dependency. Offline/solo must keep working exactly as today.
- **One seam, one place** — CLI and TUI both go through `launch`/`finish`/`repo`/`registry`; don't reimplement a create/teardown/resolve path in a caller.
- **TUI imports flow one way:** `theme` is a leaf; `component`/`modal`/`pane` may build on `component` but **never import the root `tui` package** (no cycles). The root wires the pieces and routes their result messages.
- **Pickers and colour/key editors stay bespoke** — they are not `bubbles/list`. Don't fold them into it.
- **Package docs go in `doc.go`** (comment + `package` clause only), never inline in a functional file:
  ```go
  // Package worktree wraps the git worktree porcelain. ...
  package worktree
  ```
- No narrative inline comments; only non-obvious constraints. Exported types/funcs still need a doc comment (revive enforces it).

## Config & storage

`$WASA_HOME` holds the registry JSON and optional `config.json` (theme, keys,
layout), validated at startup so a typo or conflicting binding fails loudly
rather than silently mis-applying.
