//go:build !windows

// Command wasa is a terminal cockpit for working with multiple AI coding
// agents across multiple repositories at once.
package main

import (
	"os"

	"github.com/joakimcarlsson/wasa-cli/internal/cli"
)

// version is the wasa version string. It defaults to "dev" and is intended to
// be overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(cli.Run(version, os.Args[1:]))
}
