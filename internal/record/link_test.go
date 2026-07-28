package record

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joakimcarlsson/wasa-cli/internal/registry"
)

func TestSyncRemoteFallsBackForAnUnlinkedWorkspace(t *testing.T) {
	home, repoDir, ws := linkFixture(t, nil, registry.CheckpointSyncOrigin)

	if got := SyncRemote(home, repoDir, ws.ID, "origin"); got != "origin" {
		t.Errorf("SyncRemote = %q, want origin", got)
	}
	if got := SyncRemote(home, repoDir, "", "origin"); got != "origin" {
		t.Errorf("SyncRemote by path = %q, want origin", got)
	}
}

// TestSyncRemoteIgnoresTheLink is the behaviour change: a linked workspace
// that has not chosen a checkpoint destination keeps its checkpoints on
// origin, because that is where the core reads refs/wasa/* from.
func TestSyncRemoteIgnoresTheLink(t *testing.T) {
	home, repoDir, ws := linkFixture(
		t,
		testLink(),
		registry.CheckpointSyncOrigin,
	)

	if got := SyncRemote(home, repoDir, ws.ID, "origin"); got != "origin" {
		t.Errorf("SyncRemote for a linked workspace = %q, want origin", got)
	}
	if got := SyncRemote(home, repoDir, "", "origin"); got != "origin" {
		t.Errorf("SyncRemote by path = %q, want origin", got)
	}
}

// TestSyncRemoteTakesTheCoreWhenSelected covers the explicit opt-out: the
// wasa:// path is still reachable, but only for a workspace that asked for it.
func TestSyncRemoteTakesTheCoreWhenSelected(t *testing.T) {
	home, repoDir, ws := linkFixture(t, testLink(), registry.CheckpointSyncCore)

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
	home, repoDir, _ := linkFixture(t, testLink(), registry.CheckpointSyncCore)
	linked := filepath.Join(t.TempDir(), "wt")
	run(t, repoDir, "worktree", "add", "-b", "feature", linked)

	if got := SyncRemote(home, linked, "", "origin"); got != LinkedRemote {
		t.Errorf("SyncRemote from a worktree = %q, want %q", got, LinkedRemote)
	}
}

// TestPushFollowsTheSelectedDestination covers the closing checkpoint the
// finish flow writes: it travels the destination the workspace chose, so a
// workspace that opted into the control plane keeps reaching the core.
func TestPushFollowsTheSelectedDestination(t *testing.T) {
	home, repoDir, ws := linkFixture(t, testLink(), registry.CheckpointSyncCore)
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
		t.Error("a control-plane checkpoint reached origin")
	}
}

// TestPushDefaultsToOriginWhenLinked is the same flow for the default
// destination: linking alone leaves the checkpoint on origin and sends
// nothing over wasa://.
func TestPushDefaultsToOriginWhenLinked(t *testing.T) {
	home, repoDir, ws := linkFixture(
		t,
		testLink(),
		registry.CheckpointSyncOrigin,
	)
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
	if !hasRef(t, origin, ref) {
		t.Error("a linked workspace's checkpoint did not reach origin")
	}
	if hasRef(t, linked, ref) {
		t.Error("the checkpoint reached the core without being asked to")
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

// TestAPreExistingRecordSyncsToOriginWithoutAMigration is the upgrade path: a
// registry written before the destination field existed keeps its bytes and
// its link, and its checkpoints move to origin on the strength of the zero
// value alone.
func TestAPreExistingRecordSyncsToOriginWithoutAMigration(t *testing.T) {
	home, repoDir := t.TempDir(), t.TempDir()
	if resolved, err := filepath.EvalSymlinks(repoDir); err == nil {
		repoDir = resolved
	}
	run(t, repoDir, "init")
	run(t, repoDir, "commit", "--allow-empty", "-m", "root")

	id := "abc123abc123"
	legacy := fmt.Sprintf(`{"workspaces":[{"id":%q,"name":"fixture",`+
		`"repoPath":%q,"remoteURL":"","profiles":[{"name":"default"}],`+
		`"link":{"coreURL":"https://core.example",`+
		`"repoID":"01J000000000000000000000AB","slug":"acme/widgets"}}]}`,
		id, repoDir)
	path := filepath.Join(home, "registry.json")
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	if got := SyncRemote(home, repoDir, id, "origin"); got != "origin" {
		t.Errorf("SyncRemote for a pre-existing record = %q, want origin", got)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	if string(after) != legacy {
		t.Errorf("the record was rewritten:\n%s", after)
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
	if strings.Contains(string(data), "checkpointSync") {
		t.Errorf("the default destination was serialized: %s", data)
	}
}

// TestParseCheckpointSyncTakesTheTwoDestinations keeps a typo from quietly
// resolving to origin, which would leave transcripts in a code repository the
// user meant to keep them out of.
func TestParseCheckpointSyncTakesTheTwoDestinations(t *testing.T) {
	got, err := registry.ParseCheckpointSync("origin")
	if err != nil || got != registry.CheckpointSyncOrigin {
		t.Errorf("ParseCheckpointSync(origin) = (%q, %v)", got, err)
	}
	got, err = registry.ParseCheckpointSync(registry.CheckpointSyncCore)
	if err != nil || got != registry.CheckpointSyncCore {
		t.Errorf("ParseCheckpointSync(core) = (%q, %v)", got, err)
	}
	for _, bad := range []string{"", "Origin", "core", "upstream"} {
		if _, err := registry.ParseCheckpointSync(bad); err == nil {
			t.Errorf("ParseCheckpointSync(%q): want an error", bad)
		}
	}
	if got := registry.CheckpointSyncName(""); got != "origin" {
		t.Errorf("CheckpointSyncName(zero) = %q, want origin", got)
	}
	if got := registry.CheckpointSyncName(
		registry.CheckpointSyncCore,
	); got != registry.CheckpointSyncCore {
		t.Errorf("CheckpointSyncName(core) = %q", got)
	}
}

// testLink is the link record every linked fixture carries; its contents do
// not matter to destination selection, only its presence.
func testLink() *registry.Link {
	return &registry.Link{
		CoreURL: "https://core.example",
		RepoID:  "01J000000000000000000000AB",
		Slug:    "acme/widgets",
	}
}

// linkFixture builds a git repository registered as a workspace under a fresh
// $WASA_HOME, optionally linked, with checkpoints bound for sync.
func linkFixture(
	t *testing.T,
	link *registry.Link,
	sync string,
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
	ws.CheckpointSync = sync
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
