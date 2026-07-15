package launch

import (
	"fmt"
	"sort"
	"strings"

	"github.com/joakimcarlsson/wasa-cli/internal/collision"
	"github.com/joakimcarlsson/wasa-cli/internal/registry"
	"github.com/joakimcarlsson/wasa-cli/internal/worktree"
)

// collisionPreamble returns a bounded, best-effort note listing the paths
// other live worktree sessions in ws are currently editing, or "" when there
// is nothing to report (no peers, no peer has changes, or every diff
// errored). It is the opt-in launch-time counterpart to
// record.HistoryPreamble: a new session has no changed set of its own yet, so
// every eligible session already registered in ws is a peer worth naming.
// maxPaths caps the total number of paths named across all peers, keeping the
// note short regardless of how busy the workspace is. A diff that errors for
// one peer (dirty state, a worktree mid-teardown) silently drops that peer,
// the same non-fatal contract collision detection uses everywhere else — it
// never fails the launch.
func collisionPreamble(
	home string, ws *registry.Workspace, reg *registry.Registry, maxPaths int,
) string {
	if ws == nil || reg == nil || maxPaths <= 0 {
		return ""
	}
	peers := collision.Eligible(reg.ListSessions())
	diff := func(_, worktreePath, baseCommit string) ([]string, error) {
		return worktree.New(ws.RepoPath, home, ws.ID).
			ChangedPaths(worktreePath, baseCommit)
	}
	changed := collision.ChangedPaths(peers, diff)
	byPeer := collision.PeerPaths(peers, changed, ws.ID, "", maxPaths)
	if len(byPeer) == 0 {
		return ""
	}

	ids := make([]string, 0, len(byPeer))
	for id := range byPeer {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var b strings.Builder
	b.WriteString(
		"Other live sessions in this workspace are currently editing:\n",
	)
	for _, id := range ids {
		fmt.Fprintf(
			&b,
			"- %s: %s\n",
			peerLabel(reg, id),
			strings.Join(byPeer[id], ", "),
		)
	}
	return strings.TrimSpace(b.String())
}

// peerLabel names a peer session in the collision preamble: its title, or its
// branch when it has no title, or its raw session id as a last resort (a
// session vanishing between the registry read and this lookup).
func peerLabel(reg *registry.Registry, sessionID string) string {
	s, ok := reg.Session(sessionID)
	if !ok {
		return sessionID
	}
	if s.Title != "" {
		return s.Title
	}
	if s.Branch != "" {
		return s.Branch
	}
	return sessionID
}
