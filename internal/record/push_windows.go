//go:build windows

package record

import "os/exec"

// detach is a no-op on Windows, which wasa does not run natively; the
// package still compiles there for development tooling.
func detach(*exec.Cmd) {}
