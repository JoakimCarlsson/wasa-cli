package registry

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"
)

const fileName = "registry.json"

// Registry holds the persisted workspaces and sessions for a single $WASA_HOME.
// It is not safe for concurrent use. Mutations are in-memory until Save.
type Registry struct {
	dir      string
	now      func() time.Time
	ws       []*Workspace
	sess     []*Session
	onChange func()
}

// SetOnChange registers fn to run after every successful Save — the single
// choke point every registry mutation flows through before it counts. fn is
// called on the saving goroutine and must not mutate the registry.
func (r *Registry) SetOnChange(fn func()) { r.onChange = fn }

type document struct {
	Workspaces []*Workspace `json:"workspaces"`
	Sessions   []*Session   `json:"sessions"`
}

// Open loads the registry stored under dir. A missing state file is not an
// error: it yields an empty registry that Save will create on first write.
func Open(dir string) (*Registry, error) {
	r := &Registry{dir: dir, now: time.Now}

	data, err := os.ReadFile(filepath.Join(dir, fileName))
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}

	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	r.ws = doc.Workspaces
	r.sess = doc.Sessions
	return r, nil
}

// Save writes the registry to its state file. It writes to a temporary file in
// the same directory and renames it into place so a partial write never
// corrupts existing state.
func (r *Registry) Save() error {
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return err
	}

	doc := document{Workspaces: r.ws, Sessions: r.sess}
	if doc.Workspaces == nil {
		doc.Workspaces = []*Workspace{}
	}
	if doc.Sessions == nil {
		doc.Sessions = []*Session{}
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(r.dir, fileName+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}

	if err := os.Rename(tmpName, filepath.Join(r.dir, fileName)); err != nil {
		return err
	}
	if r.onChange != nil {
		r.onChange()
	}
	return nil
}

// ListWorkspaces returns the workspaces sorted most-recently-used first. An
// empty registry returns an empty, non-nil slice and never an error.
func (r *Registry) ListWorkspaces() []*Workspace {
	out := make([]*Workspace, len(r.ws))
	copy(out, r.ws)
	slices.SortStableFunc(out, func(a, b *Workspace) int {
		return b.LastUsedAt.Compare(a.LastUsedAt)
	})
	return out
}

// ListSessions returns the persisted sessions in storage order. An empty
// registry returns an empty, non-nil slice and never an error.
func (r *Registry) ListSessions() []*Session {
	out := make([]*Session, len(r.sess))
	copy(out, r.sess)
	return out
}

// Workspace returns the workspace with the given id, if present.
func (r *Registry) Workspace(id string) (*Workspace, bool) {
	for _, w := range r.ws {
		if w.ID == id {
			return w, true
		}
	}
	return nil, false
}

// Session returns the session with the given id, if present.
func (r *Registry) Session(id string) (*Session, bool) {
	for _, s := range r.sess {
		if s.ID == id {
			return s, true
		}
	}
	return nil, false
}

// RemoveSession deletes the session with the given id from the registry,
// reporting whether it was found. It is the terminal step of the finish
// lifecycle: once removed, the session no longer appears as active. This is
// distinct from MarkExited, which keeps the session but records that its tmux
// died.
func (r *Registry) RemoveSession(id string) bool {
	for i, s := range r.sess {
		if s.ID == id {
			r.sess = slices.Delete(r.sess, i, i+1)
			return true
		}
	}
	return false
}

// RemoveWorkspace drops the workspace with id from the registry, returning
// whether one was removed. It removes only the workspace record; the caller is
// responsible for first tearing down any sessions that reference it, so it never
// leaves a session pointing at a workspace that no longer exists.
func (r *Registry) RemoveWorkspace(id string) bool {
	for i, w := range r.ws {
		if w.ID == id {
			r.ws = slices.Delete(r.ws, i, i+1)
			return true
		}
	}
	return false
}

// EnsureWorkspace returns the workspace for the repository identified by its
// canonical path and remote, registering it with a single default profile when
// it is not yet known. The boolean reports whether a new workspace was created.
func (r *Registry) EnsureWorkspace(
	canonicalRepoPath, remoteURL, name string,
) (*Workspace, bool) {
	id := WorkspaceID(canonicalRepoPath, remoteURL)
	if w, ok := r.Workspace(id); ok {
		return w, false
	}

	now := r.now()
	w := &Workspace{
		ID:         id,
		Name:       name,
		RepoPath:   canonicalRepoPath,
		RemoteURL:  remoteURL,
		Profiles:   []Profile{defaultProfile()},
		LastUsedAt: now,
		CreatedAt:  now,
	}
	r.ws = append(r.ws, w)
	return w, true
}

// AddSession appends a session, defaulting its id, status and creation time when
// unset, and updates its workspace's LastUsedAt to mark a session create.
func (r *Registry) AddSession(s *Session) {
	if s.ID == "" {
		s.ID = NewSessionID()
	}
	if s.Status == "" {
		s.Status = StatusRunning
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = r.now()
	}
	r.sess = append(r.sess, s)
	r.touch(s.WorkspaceID)
}

// MarkAttached updates the LastUsedAt of the workspace owning the given session,
// recording an attach. It reports whether the session was found.
func (r *Registry) MarkAttached(sessionID string) bool {
	for _, s := range r.sess {
		if s.ID == sessionID {
			return r.touch(s.WorkspaceID)
		}
	}
	return false
}

// MarkExited sets the given session's status to exited, recording a kill. It
// reports whether the session was found. Unlike Reconcile it does not probe
// tmux; the caller has already stopped the session.
func (r *Registry) MarkExited(sessionID string) bool {
	for _, s := range r.sess {
		if s.ID == sessionID {
			s.Status = StatusExited
			return true
		}
	}
	return false
}

// Reconcile marks every running session whose tmux session no longer exists as
// exited, using has to probe tmux. A probe error leaves that session unchanged.
// It reports whether any session's status changed.
func (r *Registry) Reconcile(has func(tmuxName string) (bool, error)) bool {
	changed := false
	for _, s := range r.sess {
		if s.Status != StatusRunning {
			continue
		}
		ok, err := has(s.TmuxName)
		if err != nil {
			continue
		}
		if !ok {
			s.Status = StatusExited
			changed = true
		}
	}
	return changed
}

func (r *Registry) touch(workspaceID string) bool {
	if w, ok := r.Workspace(workspaceID); ok {
		w.LastUsedAt = r.now()
		return true
	}
	return false
}
