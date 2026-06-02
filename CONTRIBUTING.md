# Contributing to wasa

Thanks for your interest in contributing! This guide covers how to set up a
development environment, the workflow we use, and what we expect in a pull
request.

## Prerequisites

- **Go 1.26+**
- **git**
- **tmux** — wasa drives sessions through tmux
- On **Windows**, work inside **WSL2** with tmux installed. There is no native
  Windows build; wasa targets Linux and macOS.

## Getting started

1. Fork the repository and clone your fork.
2. Install the development tooling:

   ```sh
   make install
   ```

   This installs `goimports`, `golines`, and `golangci-lint` into your Go bin
   directory.

3. Build and run to confirm your setup works:

   ```sh
   make build
   make run
   ```

## Development workflow

1. Create a feature branch off `main`:

   ```sh
   git checkout main
   git pull --ff-only
   git checkout -b feat/short-description
   ```

2. Make your change.
3. Format, lint, and test before committing:

   ```sh
   make fmt
   make lint
   go test ./...
   ```

4. Commit using [Conventional Commits](https://www.conventionalcommits.org/),
   e.g. `feat: add session search`, `fix: handle empty worktree`, or
   `docs: clarify install steps`.
5. Push your branch and open a pull request into `main`.

## Pull request checklist

Before requesting review, please confirm:

- [ ] `make fmt` produces no changes
- [ ] `make lint` passes
- [ ] `go test ./...` passes
- [ ] New or updated tests cover your change where it makes sense
- [ ] The PR title follows Conventional Commits

CI runs the same checks (goimports format check, build and test on Ubuntu and
macOS, and golangci-lint). All checks must pass before a PR can merge.

## Reporting security issues

This project intentionally does **not** include a `SECURITY.md`. If you discover
a security issue, please report it directly to the maintainer,
[@JoakimCarlsson](https://github.com/JoakimCarlsson), via GitHub rather than
opening a public issue.

## Code of conduct

By participating in this project you agree to abide by our
[Code of Conduct](CODE_OF_CONDUCT.md).
