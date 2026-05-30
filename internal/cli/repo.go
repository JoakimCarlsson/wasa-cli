package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/joakimcarlsson/wasa/internal/registry"
)

// currentRepo resolves the git repository containing the working directory and
// returns its canonical absolute path and primary remote URL. The path is the
// stable identity passed to the registry; the remote URL is empty when the
// repository has no remote.
func currentRepo() (repoPath, remoteURL string, err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	return resolveRepo(cwd)
}

// resolveRepo resolves the git repository containing dir and returns its
// canonical absolute path and primary remote URL. It is the seam shared by the
// in-repo launch (which passes the working directory) and workspace add (which
// passes an explicit path), so both derive identical workspace identities. It
// errors when dir is not inside a git repository.
func resolveRepo(dir string) (repoPath, remoteURL string, err error) {
	top, err := repoToplevel(dir)
	if err != nil {
		return "", "", err
	}
	return canonical(top), repoRemoteURL(top), nil
}

// registerRepo registers the repository identified by repoPath and remoteURL in
// reg, returning its workspace and whether it was newly created. It is the
// single registration code path: both in-repo auto-registration and workspace
// add route through it, so a repository always resolves to the same
// content-addressed id with the same default profile regardless of how it was
// registered.
func registerRepo(
	reg *registry.Registry,
	repoPath, remoteURL string,
) (*registry.Workspace, bool) {
	return reg.EnsureWorkspace(repoPath, remoteURL, filepath.Base(repoPath))
}

func repoToplevel(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository: %s", dir)
	}
	return strings.TrimSpace(string(out)), nil
}

// repoRemoteURL returns the URL of the origin remote, falling back to the first
// configured remote. It returns an empty string when the repository has no
// remote.
func repoRemoteURL(dir string) string {
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

func wasaHome() string {
	if h := os.Getenv("WASA_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".wasa"
	}
	return filepath.Join(home, ".wasa")
}
