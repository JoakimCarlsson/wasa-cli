// Package registry is wasa's persistent, repo-keyed data model. It stores
// workspaces (a per-repository scope) and sessions (one running agent in one
// worktree on one branch) as a single JSON document under $WASA_HOME, keyed by a
// content-addressed workspace identifier so that re-running inside the same
// repository always resolves to the same workspace. It reconciles persisted
// sessions against tmux on startup and enumerates workspaces most-recently-used
// first.
package registry

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// Session status values. At minimum a session is either running or has exited.
const (
	StatusRunning = "running"
	StatusExited  = "exited"
)

// DefaultProfileName is the name given to the single profile created when a
// repository is auto-registered. Full profile semantics arrive later; this is a
// placeholder so every workspace owns at least one profile.
const DefaultProfileName = "default"

const (
	idLen    = 12
	shortLen = 8
)

// Profile is a named configuration scope within a workspace. Only Name is
// meaningful today; env, envFiles, agentConfigDir and postWorktreeHook arrive in
// a later issue. EnvFiles, when populated, holds paths to env files and never
// their inlined contents.
type Profile struct {
	Name     string   `json:"name"`
	EnvFiles []string `json:"envFiles,omitempty"`
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

// Session is one running agent in one worktree on one branch, owned by a
// workspace.
type Session struct {
	ID           string    `json:"id"`
	WorkspaceID  string    `json:"workspaceID"`
	ProfileName  string    `json:"profileName"`
	Title        string    `json:"title"`
	Program      string    `json:"program"`
	Branch       string    `json:"branch"`
	WorktreePath string    `json:"worktreePath"`
	TmuxName     string    `json:"tmuxName"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
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
