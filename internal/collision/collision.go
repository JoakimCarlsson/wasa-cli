package collision

import (
	"sort"

	"github.com/joakimcarlsson/wasa-cli/internal/registry"
)

// DiffSource resolves a worktree session's changed paths — committed and
// still-uncommitted — against its base commit. It is the one point through
// which collision detection reads git, so every caller (the cockpit tick, a
// unit test with synthetic data) goes through the same seam. An error (dirty
// git state, a worktree mid-teardown, ...) is never fatal to the caller: the
// session is simply dropped from comparison, the same contract as recording.
type DiffSource func(workspaceID, worktreePath, baseCommit string) ([]string, error)

// Overlap names one other live session sharing changed paths with a session,
// and the paths they share, sorted for stable rendering.
type Overlap struct {
	SessionID string
	Paths     []string
}

// Eligible narrows a session list to the ones collision detection compares:
// live worktree sessions carrying the branch, worktree and base commit a diff
// needs. A paused session's worktree is gone (a diff would only error), and a
// plain session (no WorktreePath/BaseCommit) is excluded — the same filter the
// cockpit's churn tick already applies.
func Eligible(sessions []*registry.Session) []*registry.Session {
	var out []*registry.Session
	for _, s := range sessions {
		if s.Status == registry.StatusPaused {
			continue
		}
		if s.Branch == "" || s.WorktreePath == "" || s.BaseCommit == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// ChangedPaths resolves the changed-path set of every eligible session via
// diff, skipping — never failing on — a session whose diff errors. It is the
// one place both the cockpit indicator and the launch-time injection pull
// changed-path data from, so a session's diff is read once per tick and both
// features consume the same result.
func ChangedPaths(
	sessions []*registry.Session, diff DiffSource,
) map[string][]string {
	changed := make(map[string][]string, len(sessions))
	for _, s := range Eligible(sessions) {
		paths, err := diff(s.WorkspaceID, s.WorktreePath, s.BaseCommit)
		if err != nil {
			continue
		}
		changed[s.ID] = paths
	}
	return changed
}

// Compute intersects the changed-path sets of live worktree sessions within
// the same workspace and returns, for each session that shares at least one
// path with another live session, the other colliding session(s) and the
// paths shared with each. Sessions in different workspaces are never
// compared. A session absent from changed — its diff errored, or the session
// is not eligible — never appears as a key or as a collider. The result map
// holds no entry for a session with zero collisions.
func Compute(
	sessions []*registry.Session, changed map[string][]string,
) map[string][]Overlap {
	workspaceOf := make(map[string]string, len(sessions))
	for _, s := range sessions {
		workspaceOf[s.ID] = s.WorkspaceID
	}

	pathSets := make(map[string]map[string]struct{}, len(changed))
	for id, paths := range changed {
		set := make(map[string]struct{}, len(paths))
		for _, p := range paths {
			set[p] = struct{}{}
		}
		pathSets[id] = set
	}

	ids := make([]string, 0, len(pathSets))
	for id := range pathSets {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	result := make(map[string][]Overlap)
	for i, a := range ids {
		for _, b := range ids[i+1:] {
			if workspaceOf[a] == "" || workspaceOf[a] != workspaceOf[b] {
				continue
			}
			shared := sharedPaths(pathSets[a], pathSets[b])
			if len(shared) == 0 {
				continue
			}
			result[a] = append(
				result[a], Overlap{SessionID: b, Paths: shared},
			)
			result[b] = append(
				result[b], Overlap{SessionID: a, Paths: shared},
			)
		}
	}
	return result
}

// PeerPaths returns the paths other live sessions in workspaceID are
// currently editing, keyed by session ID, excluding excludeSessionID —
// typically the session about to be created, which has no changed set of its
// own yet. It reads the same changed map Compute consumes, so the launch-time
// injection and the cockpit indicator never disagree about what "currently
// edited" means. maxPaths caps the total number of paths returned across all
// peers (0 means unbounded), keeping the seeded note short.
func PeerPaths(
	sessions []*registry.Session,
	changed map[string][]string,
	workspaceID, excludeSessionID string,
	maxPaths int,
) map[string][]string {
	if workspaceID == "" {
		return nil
	}
	workspaceOf := make(map[string]string, len(sessions))
	for _, s := range sessions {
		workspaceOf[s.ID] = s.WorkspaceID
	}

	ids := make([]string, 0, len(changed))
	for id := range changed {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	result := make(map[string][]string)
	total := 0
	for _, id := range ids {
		if id == excludeSessionID || workspaceOf[id] != workspaceID {
			continue
		}
		paths := append([]string(nil), changed[id]...)
		sort.Strings(paths)
		for _, p := range paths {
			if maxPaths > 0 && total >= maxPaths {
				break
			}
			result[id] = append(result[id], p)
			total++
		}
	}
	return result
}

// sharedPaths returns the sorted intersection of two path sets.
func sharedPaths(a, b map[string]struct{}) []string {
	var shared []string
	for p := range a {
		if _, ok := b[p]; ok {
			shared = append(shared, p)
		}
	}
	sort.Strings(shared)
	return shared
}
