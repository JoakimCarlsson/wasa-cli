//go:build !windows

package record

import (
	"os/exec"
	"syscall"
)

// detach puts the command in its own session so it survives the hook
// process being cancelled by the agent.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
