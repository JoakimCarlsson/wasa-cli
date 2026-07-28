package record

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joakimcarlsson/wasa-cli/internal/registry"
)

func TestSyncRemoteFallsBackForAnUnlinkedWorkspace(t *testing.T) {
	home, repoDir, ws := linkFixture(t, nil)

	if got := SyncRemote(home, repoDir, ws.ID, "origin"); got != "origin" {
		t.Errorf("SyncRemote = %q, want origin", got)
	}
	if got := SyncRemote(home, repoDir, "", "origin"); got != "origin" {
		t.Errorf("SyncRemote by path = %q, want origin", got)
	}
}

func TestSyncRemoteTakesTheLinkedRemote(t *testing.T) {
	home, repoDir, ws := linkFixture(t, &registry.Link{
		CoreURL: "https://core.example",
		RepoID:  "01J000000000000000000000AB",
		Slug:    "acme/widgets",
	})

	if got := SyncRemote(home, repoDir, ws.ID, "origin"); got != LinkedRemote {
		t.Errorf("SyncRemote = %q, want %q", got, LinkedRemote)
	}
	if got := SyncRemote(home, repoDir, "", "origin"); got != LinkedRemote {
		t.Errorf("SyncRemote by path = %q, want %q", got, LinkedRemote)
	}
}

// TestSyncRemoteResolvesAWorktreeToItsMainCheckout pins the case a real
// session hits: the recorder pushes from inside a worktree under $WASA_HOME,
// which is not the path the workspace is registered under.
func TestSyncRemoteResolvesAWorktreeToItsMainCheckout(t *testing.T) {
	home, repoDir, _ := linkFixture(t, &registry.Link{
		CoreURL: "https://core.example",
		RepoID:  "01J000000000000000000000AB",
		Slug:    "acme/widgets",
	})
	linked := filepath.Join(t.TempDir(), "wt")
	run(t, repoDir, "worktree", "add", "-b", "feature", linked)

	if got := SyncRemote(home, linked, "", "origin"); got != LinkedRemote {
		t.Errorf("SyncRemote from a worktree = %q, want %q", got, LinkedRemote)
	}
}

// TestPushFollowsTheLink covers the closing checkpoint the finish flow
// writes: it travels the linked remote, not origin, so a linked session's
// last record reaches the core like every checkpoint before it.
func TestPushFollowsTheLink(t *testing.T) {
	home, repoDir, ws := linkFixture(t, &registry.Link{
		CoreURL: "https://core.example",
		RepoID:  "01J000000000000000000000AB",
		Slug:    "acme/widgets",
	})
	origin, linked := t.TempDir(), t.TempDir()
	run(t, origin, "init", "-q", "--bare")
	run(t, linked, "init", "-q", "--bare")
	run(t, repoDir, "remote", "add", "origin", origin)
	run(t, repoDir, "remote", "add", LinkedRemote, linked)
	ref := "refs/wasa/checkpoints/ab/01J000000000000000000000AB"
	run(t, repoDir, "update-ref", ref, "HEAD")

	if err := Push(home, repoDir, ws.ID, ref); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if !hasRef(t, linked, ref) {
		t.Error("the checkpoint did not reach the linked remote")
	}
	if hasRef(t, origin, ref) {
		t.Error("a linked workspace's checkpoint reached origin")
	}
}

func hasRef(t *testing.T, dir, ref string) bool {
	t.Helper()
	out, err := exec.Command(
		"git", "-C", dir, "for-each-ref", "--format=%(refname)", ref,
	).Output()
	if err != nil {
		t.Fatalf("for-each-ref: %v", err)
	}
	return strings.TrimSpace(string(out)) == ref
}

func TestSyncRemoteFallsBackWithoutAHomeOrARepo(t *testing.T) {
	if got := SyncRemote("", t.TempDir(), "", "origin"); got != "origin" {
		t.Errorf("SyncRemote without a home = %q, want origin", got)
	}
	if got := SyncRemote(t.TempDir(), "", "", "origin"); got != "origin" {
		t.Errorf("SyncRemote without a repo = %q, want origin", got)
	}
}

// TestUnlinkedWorkspaceJSONIsUnchanged is the opt-in guarantee as a document:
// a workspace that was never linked serializes without a link key at all, so
// a registry written by a wasa that knows about links is byte-for-byte what
// one written before them is.
func TestUnlinkedWorkspaceJSONIsUnchanged(t *testing.T) {
	data, err := json.Marshal(&registry.Workspace{ID: "abc"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "link") {
		t.Errorf("an unlinked workspace serialized a link: %s", data)
	}
}

// linkFixture builds a git repository registered as a workspace under a fresh
// $WASA_HOME, optionally linked.
func linkFixture(
	t *testing.T,
	link *registry.Link,
) (home, repoDir string, ws *registry.Workspace) {
	t.Helper()
	home = t.TempDir()
	repoDir = t.TempDir()
	if resolved, err := filepath.EvalSymlinks(repoDir); err == nil {
		repoDir = resolved
	}
	run(t, repoDir, "init")
	run(t, repoDir, "commit", "--allow-empty", "-m", "root")

	reg, err := registry.Open(home)
	if err != nil {
		t.Fatalf("registry.Open: %v", err)
	}
	ws, _ = reg.EnsureWorkspace(repoDir, "", "fixture")
	ws.Link = link
	if err := reg.Save(); err != nil {
		t.Fatalf("registry.Save: %v", err)
	}
	return home, repoDir, ws
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=wasa", "GIT_AUTHOR_EMAIL=wasa@localhost",
		"GIT_COMMITTER_NAME=wasa", "GIT_COMMITTER_EMAIL=wasa@localhost",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}
