// Package repo is the shared seam that resolves a directory to the git
// repository identity wasa registers as a workspace. Both the CLI (in-repo
// launch and workspace add) and the cockpit create flow route through it so a
// repository always resolves to the same canonical path, remote URL and
// therefore the same content-addressed workspace id, regardless of how it was
// reached. Duplicating this resolution is what would let the id drift into a
// second workspace for the same repository.
package repo

import (
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/joakimcarlsson/wasa/internal/registry"
	"github.com/joakimcarlsson/wasa/internal/worktree"
)

// Resolve resolves the git repository containing dir and returns its canonical
// absolute path and primary remote URL. The path is the stable identity passed
// to the registry; the remote URL is empty when the repository has no remote. It
// errors when dir is not inside a git repository.
func Resolve(dir string) (repoPath, remoteURL string, err error) {
	top, err := worktree.Toplevel(dir)
	if err != nil {
		return "", "", err
	}
	return canonical(top), originRemoteURL(top), nil
}

// Register registers the repository identified by repoPath and remoteURL in reg,
// returning its workspace and whether it was newly created. It is the single
// registration code path shared across wasa, so a repository always resolves to
// the same content-addressed id with the same default profile regardless of how
// it was registered. The caller persists reg when the boolean reports a new
// workspace.
func Register(
	reg *registry.Registry,
	repoPath, remoteURL string,
) (*registry.Workspace, bool) {
	return reg.EnsureWorkspace(repoPath, remoteURL, filepath.Base(repoPath))
}

// originRemoteURL returns the URL of the origin remote, falling back to the
// first configured remote. It returns an empty string when the repository has no
// remote.
func originRemoteURL(dir string) string {
	if u := remoteURL(dir, "origin"); u != "" {
		return u
	}
	out, err := exec.Command("git", "-C", dir, "remote").Output()
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			return remoteURL(dir, name)
		}
	}
	return ""
}

func remoteURL(dir, name string) string {
	out, err := exec.Command(
		"git", "-C", dir, "config", "--get", "remote."+name+".url",
	).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// canonical resolves p to an absolute, symlink-free path so the same repository
// always produces the same string regardless of how it was reached.
func canonical(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	return p
}
