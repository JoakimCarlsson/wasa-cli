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

// ErrWorktreeExists reports that a worktree already occupies branch's computed
// path — typically a session from an earlier, undeleted run of the same branch.
// Callers use errors.As to detect the collision and decide whether to clear the
// existing worktree and retry Add, or to surface an actionable message instead
// of git's raw `fatal: ... already exists`.
type ErrWorktreeExists struct {
	// Branch is the branch Add was asked to create a worktree for.
	Branch string
	// Path is the worktree path already occupying that branch's slot.
	Path string
}

func (e *ErrWorktreeExists) Error() string {
	return fmt.Sprintf(
		"worktree for branch %q already exists at %s", e.Branch, e.Path,
	)
}

// Add creates a worktree for branch at its computed path, creating the branch
// from the current HEAD when it does not already exist, and returns the path.
// When a worktree already occupies that path or is already registered against
// branch, Add returns *ErrWorktreeExists instead of running git and surfacing
// its raw failure, so a caller can offer to clear the stale worktree and retry.
func (m *Manager) Add(branch string) (string, error) {
	if branch == "" {
		return "", errors.New("branch must not be empty")
	}

	path := m.Path(branch)
	if list, err := m.List(); err == nil {
		for _, w := range list {
			if w.Path == path || w.Branch == branch {
				return "", &ErrWorktreeExists{Branch: branch, Path: w.Path}
			}
		}
	}
	if _, err := os.Stat(path); err == nil {
		return "", &ErrWorktreeExists{Branch: branch, Path: path}
	}

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
		if isAlreadyExistsErr(err) {
			return "", &ErrWorktreeExists{Branch: branch, Path: path}
		}
		return "", err
	}
	return path, nil
}

// isAlreadyExistsErr reports whether git's worktree add failure is the
// already-exists / already-checked-out collision, as a fallback classifier
// for cases the proactive List/Stat check in Add did not catch (e.g. a race
// with another process).
func isAlreadyExistsErr(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "already exists") ||
		strings.Contains(msg, "already checked out")
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
			_, _ = m.git(
				"worktree",
				"prune",
			) // already gone; drop stale metadata
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

// NumstatEntry is one line of `git diff --numstat` output: the path changed
// and its added/removed line counts. Added and Removed are both 0 for a
// binary-only change, matching parseNumstat's "-"-column handling.
type NumstatEntry struct {
	Path    string
	Added   int
	Removed int
}

// Numstat returns the per-path added/removed line counts of the worktree at
// worktreePath against baseCommit, both committed and still-uncommitted. It
// first runs `git add -N .` so untracked files are counted, then reads a
// single `git diff --numstat <baseCommit>`. It is the one git call behind both
// DiffNumstat (the cockpit's per-row +N/−M) and ChangedPaths (collision
// detection's per-session path set), so a caller needing both reads git once
// rather than twice.
func (m *Manager) Numstat(
	worktreePath, baseCommit string,
) ([]NumstatEntry, error) {
	if worktreePath == "" || baseCommit == "" {
		return nil, errors.New("worktree path and base commit required")
	}

	lock := indexLock(worktreePath)
	lock.Lock()
	defer lock.Unlock()

	if _, err := m.gitAt(worktreePath, "add", "-N", "."); err != nil {
		return nil, err
	}
	stat, err := m.gitAt(
		worktreePath, "--no-pager", "diff", "--numstat", baseCommit,
	)
	if err != nil {
		return nil, err
	}
	return parseNumstatEntries(stat), nil
}

// DiffNumstat returns only the added and removed line totals of the worktree at
// worktreePath against baseCommit, without the full content diff or per-path
// breakdown. It is a thin sum over Numstat, kept as its own method since most
// callers (the cockpit's per-row churn stat) want only the totals. A clean
// worktree and a binary-only change both return 0, 0.
func (m *Manager) DiffNumstat(
	worktreePath, baseCommit string,
) (added, removed int, err error) {
	entries, err := m.Numstat(worktreePath, baseCommit)
	if err != nil {
		return 0, 0, err
	}
	for _, e := range entries {
		added += e.Added
		removed += e.Removed
	}
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

// ChangedPaths returns the repository-relative paths changed in the worktree
// at worktreePath against baseCommit, both committed and still-uncommitted
// (staged, unstaged and untracked). It is a thin projection over Numstat that
// drops the added/removed counts, kept as its own method for collision
// detection and its tests. A clean worktree returns an empty, non-nil slice.
func (m *Manager) ChangedPaths(
	worktreePath, baseCommit string,
) ([]string, error) {
	entries, err := m.Numstat(worktreePath, baseCommit)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		paths = append(paths, e.Path)
	}
	return paths, nil
}

// parseNumstatEntries parses `git diff --numstat` output into one NumstatEntry
// per line. Each line is "added\tremoved\tpath", except a rename, which
// numstat renders as "added\tremoved\told => new" (or
// "added\tremoved\t{old => new}/rest" when only part of the path changed); in
// both cases the path after "=> " is taken, matching the working tree's
// current name. Binary files report "-" in both count columns, parsed as 0.
func parseNumstatEntries(out []byte) []NumstatEntry {
	entries := make([]NumstatEntry, 0)
	for line := range strings.SplitSeq(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 3 {
			continue
		}
		added, _ := strconv.Atoi(fields[0])
		removed, _ := strconv.Atoi(fields[1])
		entries = append(entries, NumstatEntry{
			Path:    renamedPath(fields[2]),
			Added:   added,
			Removed: removed,
		})
	}
	return entries
}

// renamedPath resolves a numstat path field to the file's current name,
// unwrapping git's "old => new" and "{old => new}" rename notations.
func renamedPath(field string) string {
	if idx := strings.Index(field, "=>"); idx != -1 {
		newPart := strings.TrimSpace(field[idx+len("=>"):])
		newPart = strings.TrimSuffix(newPart, "}")
		if braceIdx := strings.Index(field, "{"); braceIdx != -1 {
			prefix := field[:braceIdx]
			return prefix + newPart
		}
		return newPart
	}
	return field
}

// DeleteBranch deletes the local branch. When force is false git refuses to
// delete a branch whose commits are not merged into its upstream or HEAD (git
// branch -d); force deletes it regardless (git branch -D), discarding any
// unmerged work. The finish lifecycle force-deletes because wasa never merges,
// so a session's branch is routinely unmerged at teardown.
//
// A branch that no longer exists is treated as success: deletion's goal
// (the branch is gone) already holds, so teardown callers such as
// finish.Session must not hard-fail on a resource that's already absent.
func (m *Manager) DeleteBranch(branch string, force bool) error {
	if branch == "" {
		return errors.New("branch must not be empty")
	}

	if !m.branchExists(branch) {
		return nil
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
