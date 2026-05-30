// Package worktree wraps the git worktree porcelain. It shells out to the git
// binary rather than vendoring a git library, and routes every worktree path
// through a single placement seam (see Layout) so the on-disk layout can change
// without touching callers.
package worktree

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Manager performs git worktree operations against a single repository. The
// zero value is not usable; set RepoDir and prefer New for sane defaults.
type Manager struct {
	// Git is the git executable to invoke. Empty means "git" on PATH.
	Git string
	// RepoDir is the repository the worktree commands run against (git -C).
	RepoDir string
	// Home is the resolved $WASA_HOME passed to the Layout.
	Home string
	// Workspace is the workspace identifier passed to the Layout.
	Workspace string
	// Layout computes worktree paths. Empty means Central.
	Layout Layout
}

// New returns a Manager for repoDir using the central layout. home is the
// resolved $WASA_HOME and workspace is the workspace identifier whose worktrees
// this manager places.
func New(repoDir, home, workspace string) *Manager {
	return &Manager{
		Git:       "git",
		RepoDir:   repoDir,
		Home:      home,
		Workspace: workspace,
		Layout:    Central,
	}
}

// Worktree is one entry from git worktree list.
type Worktree struct {
	Path     string
	Head     string
	Branch   string
	Bare     bool
	Detached bool
}

// Path returns the worktree path for branch under the manager's layout. It is
// the single seam through which Add and Remove resolve paths.
func (m *Manager) Path(branch string) string {
	layout := m.Layout
	if layout == nil {
		layout = Central
	}
	return layout(Params{
		Home:      m.Home,
		Workspace: m.Workspace,
		RepoPath:  m.RepoDir,
		Branch:    branch,
	})
}

// Add creates a worktree for branch at its computed path, creating the branch
// from the current HEAD when it does not already exist, and returns the path.
func (m *Manager) Add(branch string) (string, error) {
	if branch == "" {
		return "", errors.New("branch must not be empty")
	}

	path := m.Path(branch)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create worktree parent: %w", err)
	}

	args := []string{"worktree", "add"}
	if m.branchExists(branch) {
		args = append(args, path, branch)
	} else {
		args = append(args, "-b", branch, path)
	}

	if _, err := m.git(args...); err != nil {
		return "", err
	}
	return path, nil
}

// List returns the worktrees registered for the repository.
func (m *Manager) List() ([]Worktree, error) {
	out, err := m.git("worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return parseList(out), nil
}

// Remove removes the worktree identified by target. target may be an existing
// worktree path or a branch name, in which case its path is computed via the
// manager's layout.
func (m *Manager) Remove(target string) error {
	if target == "" {
		return errors.New("target must not be empty")
	}

	path := target
	if _, err := os.Stat(target); err != nil {
		path = m.Path(target)
	}

	_, err := m.git("worktree", "remove", path)
	return err
}

func (m *Manager) branchExists(branch string) bool {
	cmd := exec.Command(
		m.bin(),
		"-C", m.RepoDir,
		"rev-parse", "--verify", "--quiet",
		"refs/heads/"+branch,
	)
	return cmd.Run() == nil
}

func (m *Manager) git(args ...string) ([]byte, error) {
	full := append([]string{"-C", m.RepoDir}, args...)
	cmd := exec.Command(m.bin(), full...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return nil, fmt.Errorf(
			"git %s: %w: %s",
			strings.Join(args, " "),
			err,
			msg,
		)
	}
	return stdout.Bytes(), nil
}

func (m *Manager) bin() string {
	if m.Git == "" {
		return "git"
	}
	return m.Git
}

func parseList(out []byte) []Worktree {
	var (
		result []Worktree
		cur    Worktree
		open   bool
	)
	flush := func() {
		if open {
			result = append(result, cur)
			cur = Worktree{}
			open = false
		}
	}

	for line := range strings.SplitSeq(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			flush()
			continue
		}
		open = true

		key, value, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			cur.Path = value
		case "HEAD":
			cur.Head = value
		case "branch":
			cur.Branch = strings.TrimPrefix(value, "refs/heads/")
		case "bare":
			cur.Bare = true
		case "detached":
			cur.Detached = true
		}
	}
	flush()
	return result
}
