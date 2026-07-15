package collision

import (
	"reflect"
	"sort"
	"testing"

	"github.com/joakimcarlsson/wasa-cli/internal/registry"
)

func worktreeSession(id, workspaceID string) *registry.Session {
	return &registry.Session{
		ID:           id,
		WorkspaceID:  workspaceID,
		Branch:       "wasa/" + id,
		WorktreePath: "/worktrees/" + id,
		BaseCommit:   "base-" + workspaceID,
		Status:       registry.StatusRunning,
	}
}

func TestEligibleExcludesPlainAndPausedSessions(t *testing.T) {
	plain := &registry.Session{ID: "plain", WorkspaceID: "ws"}
	paused := worktreeSession("paused", "ws")
	paused.Status = registry.StatusPaused
	running := worktreeSession("running", "ws")

	got := Eligible([]*registry.Session{plain, paused, running})
	if len(got) != 1 || got[0].ID != "running" {
		t.Fatalf(
			"Eligible() = %+v, want only the running worktree session",
			got,
		)
	}
}

func TestComputeFindsOverlapWithinWorkspace(t *testing.T) {
	a := worktreeSession("a", "ws1")
	b := worktreeSession("b", "ws1")
	sessions := []*registry.Session{a, b}

	changed := map[string][]string{
		"a": {"foo.go", "bar.go"},
		"b": {"foo.go", "baz.go"},
	}

	got := Compute(sessions, changed)

	wantA := []Overlap{{SessionID: "b", Paths: []string{"foo.go"}}}
	wantB := []Overlap{{SessionID: "a", Paths: []string{"foo.go"}}}
	if !reflect.DeepEqual(got["a"], wantA) {
		t.Errorf("got[a] = %+v, want %+v", got["a"], wantA)
	}
	if !reflect.DeepEqual(got["b"], wantB) {
		t.Errorf("got[b] = %+v, want %+v", got["b"], wantB)
	}
}

func TestComputeIgnoresCrossWorkspaceOverlap(t *testing.T) {
	a := worktreeSession("a", "ws1")
	b := worktreeSession("b", "ws2")
	sessions := []*registry.Session{a, b}

	changed := map[string][]string{
		"a": {"foo.go"},
		"b": {"foo.go"},
	}

	got := Compute(sessions, changed)
	if len(got) != 0 {
		t.Errorf(
			"Compute() across workspaces = %+v, want no collisions", got,
		)
	}
}

func TestComputeNoOverlapWhenPathsDisjoint(t *testing.T) {
	a := worktreeSession("a", "ws1")
	b := worktreeSession("b", "ws1")
	sessions := []*registry.Session{a, b}

	changed := map[string][]string{
		"a": {"foo.go"},
		"b": {"bar.go"},
	}

	got := Compute(sessions, changed)
	if len(got) != 0 {
		t.Errorf("Compute() disjoint paths = %+v, want no collisions", got)
	}
}

func TestComputeThreeWayOverlap(t *testing.T) {
	a := worktreeSession("a", "ws1")
	b := worktreeSession("b", "ws1")
	c := worktreeSession("c", "ws1")
	sessions := []*registry.Session{a, b, c}

	changed := map[string][]string{
		"a": {"foo.go"},
		"b": {"foo.go"},
		"c": {"foo.go"},
	}

	got := Compute(sessions, changed)
	for _, id := range []string{"a", "b", "c"} {
		if len(got[id]) != 2 {
			t.Errorf(
				"got[%s] has %d overlaps, want 2 (the other two sessions)",
				id, len(got[id]),
			)
		}
	}
}

func TestChangedPathsSkipsSessionWhoseDiffErrors(t *testing.T) {
	a := worktreeSession("a", "ws1")
	b := worktreeSession("b", "ws1")
	sessions := []*registry.Session{a, b}

	diff := func(_, worktreePath, _ string) ([]string, error) {
		if worktreePath == a.WorktreePath {
			return nil, errFake{}
		}
		return []string{"foo.go"}, nil
	}

	got := ChangedPaths(sessions, diff)
	if _, ok := got["a"]; ok {
		t.Errorf("ChangedPaths() kept session a despite its diff erroring")
	}
	if paths := got["b"]; !reflect.DeepEqual(paths, []string{"foo.go"}) {
		t.Errorf("ChangedPaths()[b] = %v, want [foo.go]", paths)
	}
}

func TestPeerPathsExcludesGivenSessionAndOtherWorkspaces(t *testing.T) {
	sessions := []*registry.Session{
		worktreeSession("new", "ws1"),
		worktreeSession("peer1", "ws1"),
		worktreeSession("peer2", "ws2"),
	}
	changed := map[string][]string{
		"new":   {"self.go"},
		"peer1": {"foo.go", "bar.go"},
		"peer2": {"foo.go"},
	}

	got := PeerPaths(sessions, changed, "ws1", "new", 0)

	if _, ok := got["new"]; ok {
		t.Errorf("PeerPaths() included the excluded session")
	}
	if _, ok := got["peer2"]; ok {
		t.Errorf("PeerPaths() included a session from a different workspace")
	}
	want := []string{"bar.go", "foo.go"}
	got1 := append([]string(nil), got["peer1"]...)
	sort.Strings(got1)
	if !reflect.DeepEqual(got1, want) {
		t.Errorf("PeerPaths()[peer1] = %v, want %v", got1, want)
	}
}

func TestPeerPathsBoundsTotalPaths(t *testing.T) {
	sessions := []*registry.Session{
		worktreeSession("new", "ws1"),
		worktreeSession("peer1", "ws1"),
		worktreeSession("peer2", "ws1"),
	}
	changed := map[string][]string{
		"peer1": {"a.go", "b.go"},
		"peer2": {"c.go", "d.go"},
	}

	got := PeerPaths(sessions, changed, "ws1", "new", 3)

	total := 0
	for _, paths := range got {
		total += len(paths)
	}
	if total != 3 {
		t.Errorf("PeerPaths() returned %d total paths, want capped at 3", total)
	}
}

type errFake struct{}

func (errFake) Error() string { return "fake diff error" }
