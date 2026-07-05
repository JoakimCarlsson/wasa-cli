package registry

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Session status values. A session is running, has exited, or is paused — soft-
// stopped with its tmux and worktree freed but its branch and record kept so it
// can be resumed.
const (
	StatusRunning = "running"
	StatusExited  = "exited"
	StatusPaused  = "paused"
)

// DefaultProfileName is the name given to the single profile created when a
// repository is auto-registered. Full profile semantics arrive later; this is a
// placeholder so every workspace owns at least one profile.
const DefaultProfileName = "default"

const (
	idLen    = 12
	shortLen = 8
)

// Profile is a named configuration scope within a workspace: the environment
// and per-program account a session launched under it should use. A workspace
// holds one or more profiles and its first is the default.
//
// Env holds environment variables injected into the session verbatim. EnvFiles
// holds paths to .env files loaded at session launch; only the paths are
// persisted here, never the secret values they contain. AgentConfigDir, when
// set, overrides the launched program's config/home directory by way of that
// program's config-dir environment variable, enabling a per-repository account.
// PostWorktreeHook is a command run after a worktree is created for the session.
//
// LinkPaths, CopyPaths and PortEnv are the declarative worktree bootstrap: when
// a worktree session is created, each LinkPaths entry is symlinked and each
// CopyPaths entry is copied from the repository into the new worktree (paths
// relative to the repository root), and when PortEnv is set a free local TCP
// port is allocated and injected into that environment variable. Link is for
// large regenerable trees (node_modules, .venv, target) where a symlink avoids
// a multi-GB copy; copy is for files the session edits independently (.env,
// .npmrc). They are all opt-in: a profile with none set bootstraps a worktree
// exactly as before. The PostWorktreeHook remains for anything not expressible
// declaratively and sees the allocated port in its environment.
type Profile struct {
	Name             string            `json:"name"`
	Env              map[string]string `json:"env,omitempty"`
	EnvFiles         []string          `json:"envFiles,omitempty"`
	AgentConfigDir   string            `json:"agentConfigDir,omitempty"`
	PostWorktreeHook string            `json:"postWorktreeHook,omitempty"`
	LinkPaths        []string          `json:"linkPaths,omitempty"`
	CopyPaths        []string          `json:"copyPaths,omitempty"`
	PortEnv          string            `json:"portEnv,omitempty"`
}

// Workspace is a per-repository scope. Its ID is content-addressed from the
// repository's canonical path and primary remote, so it is stable across runs.
// LastUsedAt drives most-recently-used ordering and updates only on session
// create and on attach, never via a background watcher.
type Workspace struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	RepoPath   string    `json:"repoPath"`
	RemoteURL  string    `json:"remoteURL"`
	Profiles   []Profile `json:"profiles"`
	LastUsedAt time.Time `json:"lastUsedAt"`
	CreatedAt  time.Time `json:"createdAt"`
}

// Session is one running agent, owned by a workspace. It comes in two shapes,
// distinguished by whether Branch and WorktreePath are set:
//
//   - A worktree session runs in a git worktree created on Branch under
//     $WASA_HOME; Branch and WorktreePath are both set and the agent runs in
//     WorktreePath.
//   - A plain session runs the program directly in WorkingDir with no branch and
//     no worktree; Branch and WorktreePath are empty. WorkspaceID is empty when
//     the session was launched outside any registered repository, in which case
//     it carries no profile and no profile environment.
//
// BaseCommit is the repository HEAD a worktree session branched from, captured
// at creation so the cockpit can diff the worktree against it. It is optional:
// plain sessions never set it and a registry written before it existed loads
// with it empty.
type Session struct {
	ID           string    `json:"id"`
	WorkspaceID  string    `json:"workspaceID,omitempty"`
	ProfileName  string    `json:"profileName,omitempty"`
	Title        string    `json:"title"`
	Program      string    `json:"program"`
	Branch       string    `json:"branch,omitempty"`
	WorktreePath string    `json:"worktreePath,omitempty"`
	WorkingDir   string    `json:"workingDir,omitempty"`
	BaseCommit   string    `json:"baseCommit,omitempty"`
	TmuxName     string    `json:"tmuxName"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
}

// DefaultProfile returns the workspace's default profile, which is its first.
// It reports false when the workspace holds no profiles, which a well-formed
// workspace never does.
func (w *Workspace) DefaultProfile() (Profile, bool) {
	if len(w.Profiles) == 0 {
		return Profile{}, false
	}
	return w.Profiles[0], true
}

// SelectProfile returns the profile named name, or the default profile when
// name is empty. An unknown name is reported as an error so a typo never
// silently falls back to the default.
func (w *Workspace) SelectProfile(name string) (Profile, error) {
	if name == "" {
		p, ok := w.DefaultProfile()
		if !ok {
			return Profile{}, errors.New("workspace has no profiles")
		}
		return p, nil
	}
	for _, p := range w.Profiles {
		if p.Name == name {
			return p, nil
		}
	}
	return Profile{}, fmt.Errorf("unknown profile %q", name)
}

// WorkspaceID returns the content-addressed identifier for a repository
// identified by its canonical absolute path and primary remote URL. The same
// inputs always yield the same id; a different path or remote yields a different
// id. A repository with no remote still gets a stable id from its path alone.
func WorkspaceID(canonicalRepoPath, remoteURL string) string {
	sum := sha256.Sum256([]byte(canonicalRepoPath + "\x00" + remoteURL))
	return hex.EncodeToString(sum[:])[:idLen]
}

// TmuxName returns the tmux session name for a session, namespaced by its
// workspace so concurrent repositories never collide. The result contains only
// hex and underscores, which tmux can address.
func TmuxName(workspaceID, sessionID string) string {
	return fmt.Sprintf("wasa_%s_%s", short(workspaceID), short(sessionID))
}

// NewSessionID returns a fresh random session identifier.
func NewSessionID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func defaultProfile() Profile {
	return Profile{Name: DefaultProfileName}
}

func short(s string) string {
	if len(s) > shortLen {
		return s[:shortLen]
	}
	return s
}
