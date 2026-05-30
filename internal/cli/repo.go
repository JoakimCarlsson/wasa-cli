package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	top, err := repoToplevel(cwd)
	if err != nil {
		return "", "", err
	}
	return canonical(top), repoRemoteURL(top), nil
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
