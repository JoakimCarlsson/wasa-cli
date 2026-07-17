package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ProjectConfigState classifies how a declared project-scoped agent config
// directory (e.g. ".claude", ".cursor") lands in a freshly added worktree under
// wasa's isolate policy: the config committed on the branch is carried by git,
// and untracked local config in the primary checkout is deliberately left
// behind rather than copied in, so a session starts from exactly the config its
// branch declares plus the worktree-local recording config wasa layers on top.
type ProjectConfigState int

const (
	// ConfigAbsent means the directory exists in neither the branch nor the
	// primary checkout, so there is nothing to carry or isolate.
	ConfigAbsent ProjectConfigState = iota
	// ConfigCarried means the directory is tracked on the branch, so git
	// checked it into the worktree — carried, by git, not by wasa.
	ConfigCarried
	// ConfigIsolated means the directory exists untracked in the primary
	// checkout and was deliberately not copied into the worktree.
	ConfigIsolated
)

func (s ProjectConfigState) String() string {
	switch s {
	case ConfigCarried:
		return "carried"
	case ConfigIsolated:
		return "isolated"
	default:
		return "absent"
	}
}

// ProjectConfigStates reports, for each repository-root-relative directory in
// dirs, how it lands in the worktree at worktreePath under the isolate policy:
// ConfigCarried when git tracks it on the branch (so it was checked into the
// worktree), ConfigIsolated when it exists untracked in the primary checkout
// (so it was left behind), and ConfigAbsent otherwise. It is the observable,
// testable seam for the policy; the untracked case does no copying by design,
// which is what "isolate" means.
func (m *Manager) ProjectConfigStates(
	worktreePath string, dirs []string,
) (map[string]ProjectConfigState, error) {
	if worktreePath == "" {
		return nil, errors.New("worktree path required")
	}

	states := make(map[string]ProjectConfigState, len(dirs))
	for _, dir := range dirs {
		tracked, err := m.dirTracked(worktreePath, dir)
		if err != nil {
			return nil, err
		}
		switch {
		case tracked:
			states[dir] = ConfigCarried
		case dirExists(filepath.Join(m.RepoDir, dir)):
			states[dir] = ConfigIsolated
		default:
			states[dir] = ConfigAbsent
		}
	}
	return states, nil
}

// dirTracked reports whether the branch checked out at worktreePath tracks any
// file under dir, via `git ls-files -- <dir>` run inside the worktree. A
// non-empty listing means git carried the directory into the worktree.
func (m *Manager) dirTracked(worktreePath, dir string) (bool, error) {
	out, err := m.gitAt(worktreePath, "ls-files", "--", dir)
	if err != nil {
		return false, err
	}
	return len(strings.TrimSpace(string(out))) > 0, nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
