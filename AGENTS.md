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
make test      # go test ./...
make env       # (Windows) re-run inside WSL; put a fresh bin/wasa first on PATH
```

**Verify a change:** get `make lint` clean, then build and drive the affected
flow. Anything touching tmux must be exercised inside WSL2 (a distro with tmux +
Go), not native Windows.

## Dependencies

One dependency is not an ordinary one: `github.com/joakimcarlsson/wasa-api/pkg/proto`,
the client wasa-api generates from its own handlers, which `link/core/` calls the
control plane through. It resolves two ways — from the sibling checkout via the
workspace `go.work` (`../AGENTS.md`) during local dev, and from the pinned
pseudo-version in `go.mod` everywhere else. Bump the pin with
`go get github.com/joakimcarlsson/wasa-api@<commit>` after a contract change
lands on wasa-api's default branch.

wasa-api is a **private** repo, so a build without the workspace — CI, a release,
a fresh clone — needs a credential for it: the workflows point git at the
`WASA_API_TOKEN` secret (a PAT with read access) and set
`GOPRIVATE=github.com/joakimcarlsson/*`. Locally, `go.work` covers it; a
standalone `GOWORK=off` build needs the same git credential.

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
- `record/` — session recording: checkpoints (meta + intent + redacted transcript) on `refs/wasa/checkpoints` via git plumbing, agent hook handling, `.claude/settings.json` hook install/remove, read-back. Best-effort by contract: recording never fails a session.
- `registry/` — persistent repo-keyed data model (workspaces + sessions) as one JSON doc under `$WASA_HOME`; reconciles against tmux on startup.
- `sessionstatus/` — per-session activity state (working/waiting/idle) and how it's derived.
- `config/` — loads `$WASA_HOME/config.json` over defaults; owns the theme/keys/layout schema; validates at startup.
- `link/` — the opt-in control-plane path, nothing on it runs offline. `link/userdirs/` resolves config (`~/.config/wasa`) vs cache (`~/.cache/wasa`) — the only place those paths are derived, and a throwaway per-process dir under `go test`. `link/identity/` is the leaf identity store the CLI and the `git-remote-wasa` helper share: named contexts + `current_context` in `contexts.json`, access/refresh tokens in the OS keychain with a `credentials.json` fallback. Every file it owns is 0600, written temp+rename, under an exclusive flock held across the whole read and write. `link/core/` talks to a wasa-api core: the loopback browser login, and the calls after it through the generated `pkg/proto` client. `link/auth/` is the seam between the two — it records a completed login and hands later commands a login JWT known to be valid, refreshing ahead in that one place. `link/repotoken/` is the per-repo credential cache: it exchanges that login JWT (RFC 8693, `POST /oauth/token`) for tokens scoped to one repo and one action, keyed by `(audience, action)` so different repos exchange in parallel while racing callers for the same key collapse onto one round-trip. A login JWT never reaches a git host. `link/gitremote/` is the `git-remote-wasa` helper that carries `refs/wasa/*` to a core: a `wasa://<owner>/<repo>` (or `wasa://<repo-id>`) remote resolves to the core's smart-HTTP git backend and the conversation is handed to `git remote-https`, so pack negotiation, protocol v2 and the option protocol stay git's own. What the helper adds is the credential — the child is pointed at wasa's `git-credential-wasa`, and because git reveals whether it is fetching or pushing only *after* its options, the helper records the direction the first revealing command implies and the credential helper reads it back to mint a pull- or push-scoped token. `wasa link` is what turns all of it on for a workspace: it resolves (or creates) the repo on the core, records `(core URL, repo ULID, slug)` as `registry.Workspace.Link`, and configures the `wasa` remote with that core pinned in `remote.wasa.wasaCore`. The pin outranks the current login context — the context says who you are, the link says where the record lives — so a workspace never follows a context switch to a different core. Linking does not move checkpoints: `record.SyncRemote` reads `registry.Workspace.CheckpointSync`, whose zero value is `origin`, so checkpoints ride along with the git host the core reads `refs/wasa/*` from unless the workspace explicitly selects `wasa` (`wasa link --checkpoints wasa`) — the deliberate choice to keep transcripts out of the code repository. Everything is opt-in and reversible: `wasa unlink` drops the link record, the remote and any control-plane checkpoint destination with it.

**Entry & UI:**

- `cmd/wasa/` — `main` (`//go:build !windows`) calls `cli.RunArgv(version, os.Args)`, which dispatches on the name the binary was invoked under before its arguments: git resolves a remote helper by executable name, so a `git-remote-wasa` symlink to `wasa` is the whole installation step (`make build` and `install.sh` both make it). `main_windows.go` is the WSL stub. `version` via `-ldflags "-X main.version=…"`.
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
