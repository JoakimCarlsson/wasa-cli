# wasa

A terminal cockpit for running and managing AI coding agents across repositories.

wasa launches each agent in its own isolated git worktree, so multiple agents can
work on different branches of the same repository without stepping on each other.
Sessions persist in the background even after you detach — via **tmux** on
Linux/macOS, and via a native pseudo-console (**ConPTY**) daemon on Windows, with
no tmux or WSL required.

## Installation

### With `go install`

Install the `wasa` binary into your Go bin directory:

```sh
go install github.com/joakimcarlsson/wasa/cmd/wasa@latest
```

Make sure your Go bin directory (`$(go env GOPATH)/bin`) is on your `PATH`, then
run `wasa`.

### From source with `make env`

Clone the repository and let `make env` build `wasa` and put it on your `PATH`:

```sh
git clone https://github.com/joakimcarlsson/wasa
cd wasa
make env
```

`make env` builds the binary into `./bin` and adds that directory to your `PATH` —
to `~/.profile`, `~/.bashrc` and `~/.zshrc` on Linux/macOS, and to your user `PATH`
on Windows. Open a new terminal afterwards and run `wasa`.

### Prebuilt binary

Download a prebuilt binary for your platform from the
[Releases](https://github.com/joakimcarlsson/wasa/releases) page and place it on
your `PATH`.

## Usage

Run `wasa` with no arguments to open the interactive cockpit (TUI):

```sh
wasa
```

From the cockpit you can browse workspaces, create sessions, and attach to running
agents. The same operations are available as subcommands for scripting:

| Command     | Description                                                       |
| ----------- | ----------------------------------------------------------------- |
| `session`   | list and create agent sessions                                    |
| `workspace` | list and resolve per-repository workspaces                        |
| `finish`    | tear down a session: remove its worktree and delete its branch    |
| `tmux`      | spawn and attach to background sessions                           |

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

## Requirements

- **Go 1.26 or later** to build from source.
- **Linux / macOS:** [`tmux`](https://github.com/tmux/tmux) on your `PATH` for
  background sessions.
- **Windows:** Windows 10 version 1809 (build 17763) or later for the native
  ConPTY backend. No tmux or WSL required.

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
