package worktree

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// indexLocks serializes the diff commands that write a worktree's git index
// (`git add -N .`) by worktree path, so the cockpit's per-tick churn numstat and
// the selected session's full diff cannot collide on the same
// .git/worktrees/<name>/index.lock. Distinct worktrees take distinct locks and
// still run concurrently; only same-worktree operations queue. The map grows by
// at most one entry per worktree seen in the process lifetime.
var (
	indexLocksMu sync.Mutex
	indexLocks   = map[string]*sync.Mutex{}
)

// indexLock returns the lock guarding the git index of the worktree at path,
// creating it on first use.
func indexLock(path string) *sync.Mutex {
	indexLocksMu.Lock()
	defer indexLocksMu.Unlock()
	mu, ok := indexLocks[path]
	if !ok {
		mu = &sync.Mutex{}
		indexLocks[path] = mu
	}
	return mu
}

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

// Toplevel returns the absolute path of the root of the git working tree that
// contains dir, via `git -C dir rev-parse --show-toplevel`. It is the single
// path→repository resolver shared by the CLI and the cockpit, so a directory
// always maps to the same repository regardless of who asks. It errors when dir
// is not inside a git repository, does not exist, or git is unavailable, so a
// caller can treat any error as "not a repository".
func Toplevel(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository: %s", dir)
	}
	return strings.TrimSpace(string(out)), nil
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

// Remove removes the worktree identified by target. An absolute target is used
// as the worktree path verbatim; any other target is treated as a branch name
// and its path is computed via the manager's layout. Routing on filepath.IsAbs
// rather than os.Stat means an absolute path whose directory has already been
// deleted is never re-sanitized into a bogus branch segment. When force is false
// a worktree with uncommitted or untracked changes blocks the removal and git's
// error is surfaced; force passes --force so the dirty worktree is removed and
// its changes discarded. Removing an already-deleted worktree is a successful
// no-op: git's failure is swallowed and stale metadata pruned, so a teardown
// whose worktree vanished from disk still completes.
func (m *Manager) Remove(target string, force bool) error {
	if target == "" {
		return errors.New("target must not be empty")
	}

	path := target
	if !filepath.IsAbs(target) {
		path = m.Path(target)
	}

	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	if _, err := m.git(append(args, path)...); err != nil {
		if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
			_, _ = m.git("worktree", "prune") // already gone; drop stale metadata
			return nil
		}
		return err
	}
	return nil
}

// Branches lists the repository's local branch names, most-recently-committed
// first, so a picker can present the branches a session is most likely to want.
// A repository with no commits yet has no branches and returns an empty slice.
func (m *Manager) Branches() ([]string, error) {
	out, err := m.git(
		"for-each-ref",
		"--sort=-committerdate",
		"--format=%(refname:short)",
		"refs/heads",
	)
	if err != nil {
		return nil, err
	}
	var names []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

// HeadSHA returns the full object name of the repository's current HEAD — the
// commit a worktree added now branches from. It is captured at session creation
// and stored so the worktree can later be diffed against it. It errors in a
// repository with no commits yet.
func (m *Manager) HeadSHA() (string, error) {
	out, err := m.git("rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// DiffResult is a worktree's diff against its base commit: the unified diff text
// and the total added and removed line counts for the cockpit's summary line.
type DiffResult struct {
	Text    string
	Added   int
	Removed int
}

// Diff returns the diff of the worktree at worktreePath against baseCommit, the
// commit the worktree branched from. It first runs `git add -N .` inside the
// worktree so untracked files appear in the diff as additions, then captures the
// unified diff and, separately, the numstat totals for the summary. The commands
// run inside the worktree rather than the main repository, so they see the
// session's working changes.
func (m *Manager) Diff(worktreePath, baseCommit string) (DiffResult, error) {
	if worktreePath == "" || baseCommit == "" {
		return DiffResult{}, errors.New(
			"worktree path and base commit required",
		)
	}

	lock := indexLock(worktreePath)
	lock.Lock()
	defer lock.Unlock()

	if _, err := m.gitAt(worktreePath, "add", "-N", "."); err != nil {
		return DiffResult{}, err
	}
	text, err := m.gitAt(worktreePath, "--no-pager", "diff", baseCommit)
	if err != nil {
		return DiffResult{}, err
	}
	stat, err := m.gitAt(
		worktreePath, "--no-pager", "diff", "--numstat", baseCommit,
	)
	if err != nil {
		return DiffResult{}, err
	}

	added, removed := parseNumstat(stat)
	return DiffResult{Text: string(text), Added: added, Removed: removed}, nil
}

// DiffNumstat returns only the added and removed line totals of the worktree at
// worktreePath against baseCommit, without the full content diff. Like Diff it
// first runs `git add -N .` so newly created untracked files are counted, then
// sums `git diff --numstat <baseCommit>`. It is the cheap per-tick churn stat
// the cockpit computes for every worktree session row, where the full Diff is
// reserved for the one selected session. A clean worktree and a binary-only
// change both return 0, 0 (binary files report "-" in both columns and are
// skipped by parseNumstat).
func (m *Manager) DiffNumstat(
	worktreePath, baseCommit string,
) (added, removed int, err error) {
	if worktreePath == "" || baseCommit == "" {
		return 0, 0, errors.New("worktree path and base commit required")
	}

	lock := indexLock(worktreePath)
	lock.Lock()
	defer lock.Unlock()

	if _, err := m.gitAt(worktreePath, "add", "-N", "."); err != nil {
		return 0, 0, err
	}
	stat, err := m.gitAt(
		worktreePath, "--no-pager", "diff", "--numstat", baseCommit,
	)
	if err != nil {
		return 0, 0, err
	}

	added, removed = parseNumstat(stat)
	return added, removed, nil
}

// parseNumstat sums the added and removed columns of `git diff --numstat`
// output. Each line is "added\tremoved\tpath"; binary files report "-" in both
// columns, which is skipped.
func parseNumstat(out []byte) (added, removed int) {
	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if a, err := strconv.Atoi(fields[0]); err == nil {
			added += a
		}
		if r, err := strconv.Atoi(fields[1]); err == nil {
			removed += r
		}
	}
	return added, removed
}

// DeleteBranch deletes the local branch. When force is false git refuses to
// delete a branch whose commits are not merged into its upstream or HEAD (git
// branch -d); force deletes it regardless (git branch -D), discarding any
// unmerged work. The finish lifecycle force-deletes because wasa never merges,
// so a session's branch is routinely unmerged at teardown.
func (m *Manager) DeleteBranch(branch string, force bool) error {
	if branch == "" {
		return errors.New("branch must not be empty")
	}

	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := m.git("branch", flag, branch)
	return err
}

// BranchExists reports whether branch exists locally in the repository. Resume
// uses it to fail before creating anything when a recorded branch is gone,
// rather than letting Add recreate the branch from scratch.
func (m *Manager) BranchExists(branch string) bool {
	return m.branchExists(branch)
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
	return m.gitAt(m.RepoDir, args...)
}

// gitAt runs git with its working directory set to dir (git -C dir). The
// repository commands use RepoDir; the diff commands run inside the worktree,
// which is a different directory, so the two cannot share a fixed -C.
func (m *Manager) gitAt(dir string, args ...string) ([]byte, error) {
	full := append([]string{"-C", dir}, args...)
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
