//go:build !windows

package gitremote

import (
	"fmt"
	"os/exec"
	"strings"
)

// RemoteName is the git remote `wasa link` configures. A linked workspace's
// record travels through it; everything else in the repository keeps using
// whatever remotes it already had.
const RemoteName = Scheme

// CoreConfigKey is the per-remote git config value naming the core a remote
// belongs to. It is git config rather than an environment variable on purpose:
// a config value survives a plain `git push wasa` typed by hand, where an
// environment variable only survives when wasa itself spawned git.
func CoreConfigKey(remote string) string {
	return "remote." + remote + ".wasaCore"
}

// URL is the wasa remote URL addressing a repo by its `<owner>/<repo>` slug.
func URL(slug string) string {
	return Scheme + "://" + strings.Trim(slug, "/")
}

// ConfiguredCore returns the core pinned on a remote, or an empty string when
// the remote is unnamed, has no pin, or dir is not a repository. An absent pin
// is the normal case for a hand-written remote and is not an error: the caller
// falls back to the current login context.
func ConfiguredCore(dir, remote string) string {
	if remote == "" {
		return ""
	}
	out, err := exec.Command(
		"git", "-C", dir, "config", "--get", CoreConfigKey(remote),
	).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Configure points the named remote at slug on coreURL, creating it when it is
// absent and rewriting it when it is not, so linking an already-linked
// workspace re-points it rather than failing.
func Configure(dir, remote, slug, coreURL string) error {
	if remote == "" {
		return fmt.Errorf("gitremote: no remote to configure")
	}
	url := URL(slug)
	if err := git(dir, "remote", "set-url", remote, url); err != nil {
		if err := git(dir, "remote", "add", remote, url); err != nil {
			return err
		}
	}
	return git(dir, "config", CoreConfigKey(remote), coreURL)
}

// Unconfigure removes the named remote and with it the core pinned on it,
// reporting whether there was one to remove.
func Unconfigure(dir, remote string) bool {
	return git(dir, "remote", "remove", remote) == nil
}

func git(dir string, args ...string) error {
	out, err := exec.Command(
		"git", append([]string{"-C", dir}, args...)...,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"git %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)),
		)
	}
	return nil
}
