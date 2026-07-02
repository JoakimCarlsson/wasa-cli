# AGENTS.md

Instructions for AI coding agents working in `wasa-cli`. See the workspace
`../AGENTS.md` and `../VISION.md` for how this sits next to `wasa-api`.

## What this is

The wasa **runner**: a terminal cockpit that launches and supervises AI coding
agents across many repositories at once. Each session is an agent running in its
own git worktree, kept alive in tmux, shown in a Bubble Tea TUI. Standalone and
useful with no server — the control plane is a layer you `wasa link` into later,
never a dependency.

Built on **tmux**, so Linux/macOS only. The Windows entry point exists solely to
print "run wasa inside WSL2" and exit; there is no Windows implementation.

## Layout

Top-level dirs under `internal/` are subsystems. The orchestration seams each
live in one place so the CLI and the TUI invoke the same path rather than
reimplementing it.

**Orchestration seams:**

- `internal/launch/` — session create/kill orchestration. The single create flow both the CLI and TUI drive (worktree → hook → bootstrap → tmux).
- `internal/finish/` — session teardown: stop tmux, remove the worktree, delete the branch. Never merges/rebases/pushes — local artifacts only.
- `internal/backend/` — the session-backend interface (spawn/attach/inspect/kill) plus `Default`, which selects the implementation for the host. Decouples wasa from tmux.
- `internal/backend/unix/` — the tmux backend (package `tmux`, `//go:build !windows`). Shells out to the tmux binary.
- `internal/worktree/` — git worktree porcelain (shells out to `git`), routed through one placement seam.
- `internal/bootstrap/` — materializes untracked-but-needed files (deps, `.env`) into a fresh worktree; hands out an isolated dev port.
- `internal/hook/` — runs a profile's post-worktree hook (deps install, `.env`, cache warm).
- `internal/profile/` — resolves a workspace profile into the `KEY=VALUE` env injected at launch.
- `internal/repo/` — resolves a directory to its canonical git identity → the content-addressed workspace id. Both CLI and TUI route through it so the id never drifts.
- `internal/registry/` — the persistent, repo-keyed data model (workspaces + sessions) as one JSON document under `$WASA_HOME`; reconciles against tmux on startup.
- `internal/sessionstatus/` — per-session activity state (working/waiting/idle) and how it's derived (authoritative hook channel vs. best-effort pane heuristic).
- `internal/config/` — loads optional cockpit config from `$WASA_HOME/config.json` over built-in defaults; owns the theme/keys/layout schema and validates the user file at startup.

**Entry point & UI:**

- `cmd/wasa/` — `main` (build-tagged `!windows`) calls `cli.Run(version, args)`; `main_windows.go` is the WSL stub. `version` is set via `-ldflags "-X main.version=…"`.
- `internal/cli/` — top-level CLI: flag parsing, usage, subcommand dispatch.
- `internal/tui/` — the cockpit: a Bubble Tea UI, one tab per workspace, sessions with status dots, create/attach/kill actions. Drives the seams above; does not reimplement them.
- `internal/tui/theme/` — resolved lipgloss styles. A leaf package (depends only on `config` + lipgloss) so every layer can import `Theme` without an import cycle.
- `internal/tui/component/` — generic, app-agnostic building blocks (keymap, pickers, tab strip, overlay helpers). Knows nothing about registry/sessions/workspaces.
- `internal/tui/modal/` — full-screen modal screens (create form, confirm, settings editor).
- `internal/tui/pane/` — the right-pane feature machines (live preview, git diff, companion terminal).

## Package docs live in `doc.go`

Every package's doc comment sits in its own `doc.go` file (just the comment +
the `package` clause), never inline above `package foo` in a functional file.
When you add a package, add its `doc.go`; a new file in an existing package
carries no package comment.

```go
// Package worktree wraps the git worktree porcelain. ...
package worktree
```

## Hard rules

- **The CLI is standalone.** The web/control-plane is something you link into, never a build or runtime dependency. Offline/solo must keep working exactly as today.
- **One seam, one place.** CLI and TUI both go through `launch`/`finish`/`repo`/`registry` — don't reimplement a create/teardown/resolve path in a caller.
- **TUI import discipline:** `theme` is a leaf; `component`/`modal`/`pane` may build on `component` but **never import the root `tui` package** (no cycles). The root wires the pieces and routes their result messages.
- **The cockpit's pickers and colour/key editors stay bespoke** — they are not `bubbles/list`. Don't "simplify" them into it.
- **Package docs go in `doc.go`** (above).
- **Don't write narrative inline comments; only comment non-obvious constraints.** Exported types/funcs still need a doc comment (revive enforces it).

## Platform & tmux

The tmux backend is behind `//go:build !windows`. On a Windows dev box, build
and run inside WSL2 (a distro with tmux + Go). Anything that exercises tmux must
be verified there, not from native Windows.

## Config & storage

`$WASA_HOME` holds the registry JSON and optional `config.json` (theme, keys,
layout). Config is validated at startup so a typo or conflicting binding fails
loudly rather than mis-applying.

## Build / run

```sh
make install   # install golangci-lint, goimports, golines
make build     # go build -buildvcs=false -o bin/wasa ./cmd/wasa
make run       # build then run
```

Built from the workspace parent, `../go.work` resolves `wasa-api/pkg/proto` from
the sibling folder for cross-repo local dev.

## Make targets & tooling

`.golangci.yml` enables `govet`, `staticcheck`, `revive`, `gocritic`, and the
usual set; formatting is `goimports` + `golines` (80-col). Both are stricter
than a stock setup — hence the `doc.go` package comments.

```sh
make fmt       # goimports -w . && golines -m 80 -w .
make lint      # go vet ./... && golangci-lint run ./...
make env       # (Windows) re-runs itself inside WSL; puts a fresh bin/wasa first on PATH
```
