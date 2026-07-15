# wasa

A terminal cockpit for running and managing AI coding agents across repositories.

wasa launches each agent in its own isolated git worktree, so multiple agents can
work on different branches of the same repository without stepping on each other.
Sessions persist in the background even after you detach, via **tmux**. wasa runs
on **Linux and macOS**; on **Windows** it runs inside **WSL2** with tmux installed.

## What wasa is for

AI agents now write code faster than anyone can review it, and git records *what*
changed but never *why*. wasa's aim is to make that scale workable: one developer
conducts many agents in parallel, and every session is recorded into the
repository itself — the prompt that started it, the conversation that produced the
code, the commits it made — so the intent and reasoning behind any change survive,
on any clone, with no external service. Orchestration and an always-on record are
what exist today; the throughline is that the record is git-native, so what wasa
knows travels with your code and outlives wasa itself.

## Installation

### Install script (Linux / macOS)

Download the prebuilt binary for your platform and put it on your `PATH`:

```sh
curl -fsSL https://raw.githubusercontent.com/JoakimCarlsson/wasa-cli/main/install.sh | bash
```

This installs `wasa` into `~/.local/bin` (override with `BIN_DIR`) and adds that
directory to your `PATH`. Set `VERSION` to pin a specific release. On Windows, run
this inside your WSL2 distribution.

### With `go install`

Install the `wasa` binary into your Go bin directory:

```sh
go install github.com/joakimcarlsson/wasa-cli/cmd/wasa@latest
```

Make sure your Go bin directory (`$(go env GOPATH)/bin`) is on your `PATH`, then
run `wasa`.

### From source with `make env`

Clone the repository and let `make env` build `wasa` and put it on your `PATH`:

```sh
git clone https://github.com/joakimcarlsson/wasa-cli
cd wasa
make env
```

`make env` builds the binary into `./bin` and adds that directory to your `PATH` —
to `~/.profile`, `~/.bashrc` and `~/.zshrc`. Open a new terminal afterwards and run
`wasa`. On Windows, run this inside your WSL2 distribution.

### Prebuilt binary

Download a prebuilt binary for your platform from the
[Releases](https://github.com/joakimcarlsson/wasa-cli/releases) page and place it on
your `PATH`.

## Usage

Run `wasa` with no arguments to open the interactive cockpit (TUI):

```sh
wasa
```

From the cockpit you can browse workspaces, add a git repository as a workspace
(`w`) or remove one (`W`), create sessions, and attach to running agents. The
same operations are available as subcommands for scripting:

| Command       | Description                                                       |
| ------------- | ----------------------------------------------------------------- |
| `session`     | list and create agent sessions                                    |
| `workspace`   | list, add, remove and resolve per-repository workspaces            |
| `finish`      | tear down a session: remove its worktree and delete its branch    |
| `tmux`        | spawn and attach to background sessions                           |
| `checkpoints` | list or show recorded agent sessions for this repository          |
| `record`      | enable, disable or inspect repo-level session recording           |

Run `wasa --help` for the full list, and `wasa <command>` for per-command usage.

### Example: create and attach to a session

A session is fundamentally a program running in a working directory. By default
`session new` launches a **plain session** in the current directory — no branch,
no worktree — so you can point an agent at any folder, even one that is not a git
repository:

```sh
# Plain session in the current directory (or pass --dir <path>). wasa picks the
# sole agent detected on your PATH, or pass --program to choose one explicitly.
wasa session new --title "scratch chat"
```

Pass `--branch` to opt into a **worktree session**: from inside a git repository,
wasa creates the branch and a dedicated worktree for it so several agents can work
the same repo in parallel without clobbering each other:

```sh
# Worktree session on the "feature/login" branch.
wasa session new --branch feature/login --title "login flow"

# List sessions to see ids, titles, branches and status.
wasa session list

# Attach to the running session by its backend name.
wasa tmux attach --name <name>
```

When you are done, tear the session down. wasa **never merges** — `finish` removes
local artifacts only, so merge or push any work you want to keep beforehand:

```sh
wasa finish <session>
```

### Machine-readable output (`--json`)

For scripting and editor integrations, the read commands can emit structured
JSON instead of the human-formatted tables. Pass `--json` to a command and it
writes a single JSON document to stdout and nothing else; on error it writes to
stderr and exits non-zero without printing a JSON document.

```sh
wasa version --json              # { "version": "1.2.3", "contract": 1 }
wasa session list --json         # { "sessions": [ … ] }
wasa workspace list --json       # { "workspaces": [ … ] }
wasa checkpoints --json          # { "checkpoints": [ … ] }
wasa checkpoints show <id> --json
wasa config show --json          # the effective config, no leading "# path" line
```

List output is always wrapped in an object (`{"sessions": […]}`, never a bare
array) so the shape can gain fields without breaking parsers. `checkpoints`
items carry the checkpoint metadata at the top level (`sessionId`, `branch`, …)
plus a derived `state`; `checkpoints show` adds `intent` and the rendered
`transcript`.

`wasa version --json` is the discovery surface: `version` is the build version
and `contract` is an integer identifying this structured-output contract. A
consumer should read `contract` and refuse to parse output from a wasa whose
contract it does not understand. `contract` is bumped only on a
backward-incompatible change to a `--json` payload; additive fields do not bump
it.

### Session recording

wasa records agent sessions **into the repository itself** — git-natively, with
no service dependency — so "why does this code exist?" still has an answer six
months later, on any clone. Each session becomes a chain of checkpoints on a
dedicated ref, `refs/wasa/checkpoints`; every checkpoint holds the prompt that
started the session (`intent.md`), the conversation so far
(`transcript.jsonl`) and metadata linking the commits it produced
(`meta.json`).

Recording happens on three triggers, all automatic for sessions launched
through wasa (the agent's hooks are installed in the session worktree at
launch and disappear with it):

- a checkpoint per commit that lands on the session branch,
- a closing checkpoint at `wasa finish` with the final transcript and full
  commit list — a session with zero commits is still recorded,
- for the details in between, the agent's own hook events keep the record
  current.

Read it back anywhere:

```sh
wasa checkpoints                  # one line per recorded session
wasa checkpoints show <session>   # intent + meta, pages the transcript

# The record travels with the repo — fetch it on any clone:
git fetch origin refs/wasa/checkpoints:refs/wasa/checkpoints
```

Hook-driven capture works for every agent wasa launches, each through its
own native hook configuration:

| Agent       | Hook configuration                                  |
| ----------- | --------------------------------------------------- |
| Claude Code | `.claude/settings.json`                             |
| Gemini CLI  | `.gemini/settings.json` (+ `hooksConfig.enabled`)   |
| Codex CLI   | `.codex/hooks.json` (+ `[features] hooks` in TOML)  |
| Copilot CLI | `.github/hooks/wasa.json`                           |
| Cursor      | `.cursor/hooks.json`                                |

(Codex exposes no session-end hook, so an unmanaged Codex session gets
commit-linked checkpoints but closes only through `wasa finish`.)

Aider has no hook mechanism at all — its only lifecycle callback fires on
every response with no session payload — so it gets no per-commit or
live-intent capture. What is achievable is picked up passively: `wasa finish`
reads Aider's own default chat log, `.aider.chat.history.md`, for the closing
checkpoint's transcript and intent, so a session still ends up recorded, just
without the detail hook-driven agents get in between.

To also record agent sessions run **directly** in a repository — no wasa
session around them — enable repo-level recording once; it installs hooks
for every supported agent found on your PATH:

```sh
wasa record enable
wasa record status
wasa record disable
```

Unmanaged sessions land on the same ref, marked `unmanaged`, with the intent
taken from the first prompt the agent's hooks report.

Safety properties, by construction: checkpoints are written with git plumbing
only, so your branches, index, working copy and `git status` are never
touched; transcripts are redacted for common secret formats (API keys,
tokens, credentials — best-effort, on by default) before they enter the repo;
after each write the ref is pushed to `origin` when possible, and silently
skipped offline. Recording is best-effort throughout: a recorder failure logs
one warning and never fails or slows the session.

### Per-agent capabilities

Every agent wasa detects on PATH is declared once, in
`internal/agent/agent.go`, across three capabilities: config-dir isolation
(the env var a per-repo profile sets to keep one agent's account/config
separate from another repo's), an autonomy flag (the skip-permissions toggle
the create form's autonomous switch appends), and a recorder (the hook and
transcript integration above). A capability an agent genuinely lacks is a
declared absence — an empty field with a one-line rationale — rather than a
silent gap:

| Agent        | Config-dir var       | Autonomy flag                              | Recorder |
| ------------ | --------------------- | ------------------------------------------- | -------- |
| claude       | `CLAUDE_CONFIG_DIR`   | `--dangerously-skip-permissions`            | yes      |
| codex        | `CODEX_HOME`          | `--dangerously-bypass-approvals-and-sandbox`| yes      |
| copilot      | `GH_CONFIG_DIR`       | `--allow-all-tools`                         | yes      |
| gemini       | `GEMINI_CONFIG_DIR`   | `--yolo`                                    | yes      |
| cursor-agent | *none documented*     | `--force`                                   | yes      |
| aider        | *none documented*     | `--yes-always`                              | yes (finish-time only, see above) |

cursor-agent and aider have no config-dir isolation because neither
documents a directory-override env var (cursor-agent's CLI reference lists
only `CURSOR_API_KEY`; aider resolves `.aider.conf.yml` from git root/cwd/home
with no override). Two sessions for either agent against different accounts
share global config until that changes upstream.

Aider also has no CLI seeding mechanism, so a new or resumed wasa session
never hands it an initial prompt or a resume preamble the way it does for the
other five agents: `--message`/`-m` sends one message and exits chat mode
entirely, and a positional argument means "add this file", not "say this".
An Aider session always starts (or resumes) with an empty chat; type the
first prompt once it's running.


## Requirements

- **Go 1.26 or later** to build from source.
- **Linux / macOS:** [`tmux`](https://github.com/tmux/tmux) is **required** and
  must be on your `PATH` — wasa spawns background sessions through it. Install
  it with your package manager:

  ```sh
  brew install tmux        # macOS
  sudo apt install tmux    # Debian / Ubuntu
  sudo dnf install tmux    # Fedora
  sudo pacman -S tmux      # Arch
  ```

- **Windows:** wasa has no native Windows build — run it inside **WSL2**. Install
  a WSL2 distribution (e.g. Ubuntu), install `tmux` inside it, and run wasa there.
  Keep your repositories in the WSL filesystem (e.g. `~/code/...`) rather than under
  `/mnt/c/...`: the Linux filesystem is far faster and avoids git permission and
  line-ending surprises across the Windows boundary.

## Development

Common `make` targets:

```sh
make fmt     # format with goimports + golines
make lint    # go vet + golangci-lint
make build   # build ./bin/wasa
make run     # build and run the cockpit
make env     # build and add ./bin to your PATH
```

Run `make install` once to install the lint/format tooling (`golangci-lint`,
`goimports`, `golines`).

Run the tests with:

```sh
go test ./...
```

## License

This project is licensed under the MIT License — see the [LICENSE](LICENSE) file for details.
