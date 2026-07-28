package record

import (
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/joakimcarlsson/wasa-cli/internal/registry"
)

// LinkedRemote is the git remote a linked workspace's record travels through.
// `wasa link` configures it; nothing else in the repository uses it.
const LinkedRemote = "wasa"

// SyncRemote returns the remote a workspace's refs/wasa record should sync to:
// LinkedRemote when the workspace carries a link record, and fallback when it
// does not. An unlinked workspace is the whole of today's behaviour, so the
// fallback path is unchanged and reaches no network of its own.
//
// The workspace is found by id when the caller knows one — a managed session
// records it — and otherwise by resolving repoDir to the main checkout it
// belongs to and matching that path, so a push issued from inside a session's
// worktree resolves to the same workspace as one issued from the repository.
func SyncRemote(home, repoDir, workspaceID, fallback string) string {
	if home == "" {
		return fallback
	}
	reg, err := registry.Open(home)
	if err != nil {
		return fallback
	}
	if workspaceID != "" {
		if w, ok := reg.Workspace(workspaceID); ok {
			return remoteFor(w, fallback)
		}
	}
	main := mainWorktree(repoDir)
	if main == "" {
		return fallback
	}
	for _, w := range reg.ListWorkspaces() {
		if w.RepoPath == main {
			return remoteFor(w, fallback)
		}
	}
	return fallback
}

func remoteFor(w *registry.Workspace, fallback string) string {
	if w.Link == nil {
		return fallback
	}
	return LinkedRemote
}

// mainWorktree returns the canonical path of the main checkout the repository
// containing dir belongs to, which is the path a workspace is registered
// under. `git worktree list` reports the main worktree first.
func mainWorktree(dir string) string {
	if dir == "" {
		return ""
	}
	out, err := exec.Command(
		"git", "-C", dir, "worktree", "list", "--porcelain",
	).Output()
	if err != nil {
		return ""
	}
	first, _, _ := strings.Cut(string(out), "\n")
	path, ok := strings.CutPrefix(strings.TrimSpace(first), "worktree ")
	if !ok {
		return ""
	}
	return canonical(strings.TrimSpace(path))
}

// canonical resolves p the way the registry stores a repository path, so the
// two are comparable.
func canonical(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	return p
}
