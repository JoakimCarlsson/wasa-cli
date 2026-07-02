package bootstrap

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
)

// Apply materializes the declarative bootstrap of a profile into the worktree
// at worktreePath, whose repository is rooted at repoPath. Each entry in link
// and copyPaths is a path relative to the repository root: link entries are
// symlinked from the repository copy into the worktree (intended for large
// regenerable trees, where a symlink avoids a multi-GB copy), copy entries are
// copied as independent copies the session can edit without touching the
// repository (intended for files like .env).
//
// A source path that does not exist is skipped — appended to the returned
// skipped slice — rather than failing, so a profile may name paths that only
// some repositories have. A real failure (a copy or symlink that cannot be
// created for an existing source) is returned as err. Symlink targets are
// absolute, so they resolve from the worktree regardless of how it is reached.
func Apply(
	repoPath, worktreePath string,
	link, copyPaths []string,
) (skipped []string, err error) {
	for _, rel := range link {
		ok, err := linkOne(repoPath, worktreePath, rel)
		if err != nil {
			return skipped, err
		}
		if !ok {
			skipped = append(skipped, rel)
		}
	}
	for _, rel := range copyPaths {
		ok, err := copyOne(repoPath, worktreePath, rel)
		if err != nil {
			return skipped, err
		}
		if !ok {
			skipped = append(skipped, rel)
		}
	}
	return skipped, nil
}

// linkOne symlinks repoPath/rel to worktreePath/rel with an absolute target. It
// reports false (and no error) when the source does not exist, so the caller can
// record the skip and continue.
func linkOne(repoPath, worktreePath, rel string) (bool, error) {
	src := filepath.Join(repoPath, rel)
	if _, err := os.Lstat(src); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("bootstrap link %q: %w", rel, err)
	}

	dst := filepath.Join(worktreePath, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, fmt.Errorf("bootstrap link %q: %w", rel, err)
	}
	if err := os.Symlink(src, dst); err != nil {
		return false, fmt.Errorf("bootstrap link %q: %w", rel, err)
	}
	return true, nil
}

// copyOne copies repoPath/rel into worktreePath/rel as an independent copy. It
// reports false (and no error) when the source does not exist.
func copyOne(repoPath, worktreePath, rel string) (bool, error) {
	src := filepath.Join(repoPath, rel)
	info, err := os.Lstat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("bootstrap copy %q: %w", rel, err)
	}

	dst := filepath.Join(worktreePath, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, fmt.Errorf("bootstrap copy %q: %w", rel, err)
	}
	if err := copyTree(src, dst, info); err != nil {
		return false, fmt.Errorf("bootstrap copy %q: %w", rel, err)
	}
	return true, nil
}

// copyTree copies the file or directory at src (described by info) to dst,
// recursing into directories and copying file contents and permission bits.
func copyTree(src, dst string, info os.FileInfo) error {
	if info.IsDir() {
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			ei, err := e.Info()
			if err != nil {
				return err
			}
			if err := copyTree(
				filepath.Join(src, e.Name()),
				filepath.Join(dst, e.Name()),
				ei,
			); err != nil {
				return err
			}
		}
		return nil
	}
	return copyFile(src, dst, info.Mode().Perm())
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// FreePort asks the OS for an unused local TCP port by binding 127.0.0.1:0 and
// releasing it, returning the port number. Concurrent calls return distinct
// ports. There is an inherent race between releasing the listener and a program
// binding the returned port, but for handing distinct dev-server ports to
// concurrent sessions it is sufficient.
func FreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("allocate free port: %w", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
