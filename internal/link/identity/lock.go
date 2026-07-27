//go:build !windows

package identity

import (
	"os"
	"path/filepath"
)

// fileLock is an exclusive advisory lock on a sidecar of a state file. The
// sidecar is locked instead of the state file itself because an atomic
// temp+rename write replaces the file's inode: two processes locking the state
// file directly could end up holding locks on different inodes.
type fileLock struct {
	f *os.File
}

func lockState(path string) (*fileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(
		path+".lock",
		os.O_CREATE|os.O_RDWR,
		0o600,
	)
	if err != nil {
		return nil, err
	}
	if err := lockExclusive(f); err != nil {
		f.Close()
		return nil, err
	}
	return &fileLock{f: f}, nil
}

func (l *fileLock) unlock() {
	unlockFile(l.f)
	l.f.Close()
}
