## Why / Context

wasa's TUI is built on Bubble Tea v1 (`github.com/charmbracelet/bubbletea v1.3.10`, `bubbles v1.0.0`, `lipgloss v1.1.0`). The Charm v2 line has since become the maintained target: it moved to the `charm.land/*/v2` module namespace and reworked the input, cursor, and color APIs. Staying on v1 means we accumulate distance from upstream fixes and can't adopt v2-only capabilities (native cursor reporting, the refined key/mouse event model, `lipgloss` v2 color resolution). This issue upgrades the whole cockpit to v2 in one coordinated pass so the TUI stays on a supported base.

This is a pure dependency/refactor change: no user-visible behavior should change. The cockpit must look and behave exactly as it does today once the migration lands.

## What needs to happen

Migrate every TUI file off the v1 Charm modules onto the v2 ones and adjust the code to the v2 API. The dependency swap in `go.mod` is:

- `github.com/charmbracelet/bubbletea v1.3.10` → `charm.land/bubbletea/v2`
- `github.com/charmbracelet/bubbles v1.0.0` → `charm.land/bubbles/v2`
- `github.com/charmbracelet/lipgloss v1.1.0` → `charm.land/lipgloss/v2`

The import sites are contained: ~24 files under `internal/tui` (plus `internal/tui/theme`, `internal/tui/component`, `internal/tui/modal`, `internal/tui/pane`). There are no Charm imports outside `internal/tui`, so the blast radius is the TUI layer only — the seams (`launch`/`finish`/`worktree`/`record`/`registry`) are untouched.

## How it should work

- **Coordinated bump.** bubbletea, bubbles, and lipgloss must move to v2 together — v2 bubbles/lipgloss depend on v2 bubbletea. Do not attempt a partial bump.
- **Follow the upstream v1→v2 migration guide** for the exact API deltas rather than guessing. The changes that will touch our code:
  - **Key handling.** v2 replaces the single `tea.KeyMsg` with distinct `KeyPressMsg` / `KeyReleaseMsg`. Our keymap dispatch (`internal/tui/component` keymap, `filter.go`, `app.go`, the pickers, modals) reads `tea.KeyMsg`; these need updating to the v2 message types and key-string helpers.
  - **lipgloss v2 color model.** Color values are resolved against a terminal profile in v2. `internal/tui/theme` is the leaf that owns all resolved styles — concentrate the color-API migration there so the rest of the TUI keeps importing `theme.Theme` unchanged.
  - **Program construction / lifecycle.** `tea.NewProgram`, `WithAltScreen`, and the `Init/Update/View` signatures — reconcile with v2.
- **Respect the existing TUI import rule** (`AGENTS.md`): `theme` stays a leaf; `component`/`modal`/`pane` never import the root `tui` package. The migration must not introduce a cycle to work around a v2 API change.
- **Pickers and colour/key editors stay bespoke** — do not take the v2 upgrade as an opportunity to fold them into `bubbles/list`. Port them as-is onto the v2 API.
- Keep the `charmbracelet/x/ansi` dependency aligned with whatever the v2 modules pull in (v2 uses `charm.land`-namespaced x packages in places; let `go mod tidy` resolve the transitive set).

## Acceptance criteria

- [ ] `go.mod` lists `charm.land/bubbletea/v2`, `charm.land/bubbles/v2`, `charm.land/lipgloss/v2` and no longer lists any `github.com/charmbracelet/{bubbletea,bubbles,lipgloss}` v1 module.
- [ ] No source file under `internal/tui` imports a v1 Charm TUI module.
- [ ] `make build`, `make lint`, and `make test` all pass.
- [ ] Launching the cockpit renders identically to v1: tab strip, session list with status dots, create/confirm/settings modals, and the right-pane preview/diff/terminal all display and navigate as before.
- [ ] Keybindings behave unchanged (navigation, filter, create, attach, kill, quit).

## How to verify (runnable)

Run inside WSL2 (wasa is Linux/tmux-only; native Windows just prints the WSL stub):

```sh
cd wasa-cli
go mod tidy
grep -rn "charmbracelet/bubbletea\|charmbracelet/bubbles\|charmbracelet/lipgloss" internal cmd   # expect no matches
make lint
make test
make build
./bin/wasa            # open the cockpit
```

Then drive the cockpit end-to-end: create a session, attach and detach, open the settings/config modal, switch the right pane between preview / git diff / terminal, filter the session list, and kill a session. Confirm rendering and keybindings match current `main`. Only open the PR once all of the above pass.

## Dependencies / notes

Self-contained; no ordering dependency on other issues. Pure dependency migration — resist scope creep into TUI feature changes; behavior parity with current `main` is the bar.