//go:build !windows

package identity

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockExclusive(f *os.File) error {
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX)
		if err == unix.EINTR {
			continue
		}
		return err
	}
}

func unlockFile(f *os.File) {
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
