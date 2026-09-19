# AGENTS.md — wasa-cli

The wasa **runner**: a terminal cockpit that launches and supervises AI coding
agents, each in its own git worktree, kept alive in tmux, shown in a Bubble Tea
TUI. Standalone — the control plane lives on the `task/api` branch and is never
a dependency of this one. Workspace map: `../AGENTS.md`; product:
`../VISION.md`.

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
make test      # go test ./...
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
- `mcp/` — injects a profile's declared MCP servers into a session by writing the launched agent's own MCP config into the worktree. Which file, and under which key, is declared per agent in `agent/`; an agent with no MCP mechanism is skipped, never failed.
- `repo/` — resolves a directory to its canonical git identity → the content-addressed workspace id.
- `record/` — session recording: checkpoints (meta + intent + redacted transcript) on `refs/wasa/checkpoints` via git plumbing, agent hook handling, `.claude/settings.json` hook install/remove, read-back. Best-effort by contract: recording never fails a session.
- `registry/` — persistent repo-keyed data model (workspaces + sessions) as one JSON doc under `$WASA_HOME`; reconciles against tmux on startup.
- `sessionstatus/` — per-session activity state (working/waiting/idle) and how it's derived.
- `config/` — loads `$WASA_HOME/config.json` over defaults; owns the theme/keys/layout schema; validates at startup.

**Entry & UI:**

- `cmd/wasa/` — `main` (`//go:build !windows`) calls `cli.Run(version, os.Args[1:])`. `main_windows.go` is the WSL stub. `version` via `-ldflags "-X main.version=…"`.
- `cli/` — flag parsing, usage, subcommand dispatch.
- `tui/` — the cockpit (Bubble Tea): one tab per workspace, sessions with status dots, create/attach/kill. Drives the seams; never reimplements them. **The frame is full-bleed** — no pane borders, no boxed tabs: columns are separated by `layout.PaneGutter` and aligned by a shared rule (`paneHeader` on the left, `component.TabStrip` on the right). Overlays keep their border; body panes must not grow one.
- `tui/theme/` — resolved lipgloss styles, all built from `Tokens`, the semantic colour layer (`Accent`, `Muted`, `Danger`, …) that config's element-named palette resolves into. A leaf package (config + lipgloss only) so every layer imports `Theme` without an import cycle. Style a new widget from a token, never from a config field.
- `tui/layout/` — the geometry: the spacing scale (`Tight`/`Snug`/`Loose`) and `Frame`, which turns a terminal size into body height and column widths. A leaf package. Views ask for a `Frame`; they do not re-derive the arithmetic locally.
- `tui/syntax/` — Chroma wrapper. A per-file `Highlighter` renders each token with a caller-supplied base style, so highlighted code keeps the diff band behind it.
- `tui/markdown/` — Glamour wrapper with a width-keyed renderer cache, for the markdown agents write (checkpoint intent, transcript bodies).
- `tui/component/` — generic building blocks (keymap, pickers, tab strip, overlay helpers). Knows nothing about registry/sessions/workspaces.
- `tui/modal/` — full-screen modals (create form, confirm, settings editor).
- `tui/pane/` — right-pane feature machines (natively drawn session overview, git diff, live screen capture, companion terminal). `Overview` is stateless: the root projects the registry into an `OverviewSession` each frame.

## Hard rules

- **The CLI is standalone** — no control-plane code on this branch. Anything that talks to wasa-api belongs on `task/api`; offline/solo must keep working exactly as today.
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
