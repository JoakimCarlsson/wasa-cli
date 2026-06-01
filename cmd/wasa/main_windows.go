//go:build windows

// Command wasa is a terminal cockpit for working with multiple AI coding
// agents across multiple repositories at once. It is a Linux/macOS tool built
// on tmux; this Windows entry point exists only to fail with an actionable
// message instead of a cryptic build or runtime error.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "wasa runs on Linux and macOS. On Windows, install WSL2 with tmux and run wasa inside your WSL distribution.")
	os.Exit(1)
}
