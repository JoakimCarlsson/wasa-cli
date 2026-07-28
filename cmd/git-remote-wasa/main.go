//go:build !windows

// Command git-remote-wasa is git's remote helper for wasa:// remotes. git
// resolves a remote helper by executable name, so it ships as a binary of its
// own rather than as a symlink to wasa that every installation path has to
// remember to make.
package main

import (
	"os"

	"github.com/joakimcarlsson/wasa-cli/internal/cli"
)

func main() {
	os.Exit(cli.RunRemoteHelper(os.Args[1:]))
}
