package record

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// initRepo creates a git repository with one commit and returns its path.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(
		filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "a.txt")
	mustGit(t, dir, "commit", "-q", "-m", "initial")
	return dir
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitIn(dir, nil, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func mustWrite(t *testing.T, dir string, cp Checkpoint) string {
	t.Helper()
	ref, err := Write(dir, cp)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	return ref
}

func TestWriteAndReadBack(t *testing.T) {
	dir := initRepo(t)
	statusBefore := mustGit(t, dir, "status", "--porcelain")

	transcript := `{"type":"user","message":{"role":"user",` +
		`"content":"use ghp_abcdefghijklmnopqrstuvwxyz0123456789 please"}}`
	ref1 := mustWrite(t, dir, Checkpoint{
		Meta: Meta{
			SessionID:   "abc123",
			Branch:      "task/x",
			WasaVersion: "test",
		},
		Intent: "do the thing with " +
			"ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		Transcript: []byte(transcript),
	})
	ref2 := mustWrite(t, dir, Checkpoint{
		Meta: Meta{
			SessionID: "abc123", Branch: "task/x",
			Commits: []string{"deadbeef"}, WasaVersion: "test",
		},
		Intent:     "do the thing",
		Transcript: []byte(transcript),
	})

	if ref1 == ref2 {
		t.Fatalf("two checkpoints share a ref: %q", ref1)
	}
	for _, ref := range []string{ref1, ref2} {
		if !strings.HasPrefix(ref, RefPrefix+"/") {
			t.Errorf("ref %q not under %s/", ref, RefPrefix)
		}
	}
	if legacy := mustGit(
		t, dir, "for-each-ref", "--format=%(refname)", RefPrefix,
	); strings.Contains(legacy, RefPrefix+"\n") {
		t.Errorf("legacy chain ref present: %q", legacy)
	}

	if got := mustGit(t, dir, "status", "--porcelain"); got != statusBefore {
		t.Errorf("git status changed by recording: %q -> %q",
			statusBefore, got)
	}
	if branches := mustGit(
		t, dir, "for-each-ref", "--format=%(refname)", "refs/heads",
	); strings.Count(branches, "\n") != 0 {
		t.Errorf("recording created branches: %q", branches)
	}

	if stored := mustGit(t, dir, "show", ref2+":meta.json"); !strings.Contains(
		stored, `"storageVersion": "refs-1"`,
	) {
		t.Errorf("meta.json missing storageVersion: %q", stored)
	}
	intent := mustGit(t, dir, "show", ref2+":intent.md")
	if intent != "do the thing" {
		t.Errorf("intent.md = %q", intent)
	}
	firstIntent := mustGit(t, dir, "show", ref1+":intent.md")
	if strings.Contains(firstIntent, "ghp_") {
		t.Errorf("unredacted token in intent.md: %q", firstIntent)
	}
	stored := mustGit(t, dir, "show", ref2+":transcript.jsonl")
	if strings.Contains(stored, "ghp_") {
		t.Errorf("unredacted token on the ref: %q", stored)
	}
	if !strings.Contains(stored, placeholder) {
		t.Errorf("transcript missing placeholder: %q", stored)
	}

	entries, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List returned %d entries, want 1 (one per session)", len(entries))
	}
	e := entries[0]
	if e.Meta.SessionID != "abc123" || e.Meta.Branch != "task/x" {
		t.Errorf("List entry meta = %+v", e.Meta)
	}
	if len(e.Meta.Commits) != 1 || e.Meta.Commits[0] != "deadbeef" {
		t.Errorf("List entry commits = %v", e.Meta.Commits)
	}

	found, gotIntent, gotTranscript, err := Find(dir, "abc")
	if err != nil {
		t.Fatalf("Find by session: %v", err)
	}
	if found.Meta.SessionID != "abc123" || gotIntent != "do the thing" {
		t.Errorf("Find = %+v intent %q", found.Meta, gotIntent)
	}
	if strings.Contains(string(gotTranscript), "ghp_") {
		t.Errorf("Find returned unredacted transcript")
	}

	byID, _, _, err := Find(dir, found.ID)
	if err != nil || byID.ID != found.ID {
		t.Errorf("Find by ULID = %+v, %v", byID, err)
	}
}

func TestWriteDeletesLegacyChainRef(t *testing.T) {
	dir := initRepo(t)
	head := mustGit(t, dir, "rev-parse", "HEAD")
	mustGit(t, dir, "update-ref", RefPrefix, head)

	mustWrite(t, dir, Checkpoint{
		Meta: Meta{SessionID: "s1", WasaVersion: "test"},
	})

	refs := mustGit(t, dir, "for-each-ref", "--format=%(refname)", RefPrefix+"/")
	if strings.Count(strings.TrimSpace(refs), "\n") != 0 || refs == "" {
		t.Errorf("want exactly one sharded ref, got %q", refs)
	}
	if _, err := gitIn(
		dir, nil, "rev-parse", "--verify", "-q", RefPrefix,
	); err == nil {
		t.Error("legacy chain ref survived a write")
	}
}

func TestListWithoutRef(t *testing.T) {
	dir := initRepo(t)
	entries, err := List(dir)
	if err != nil || entries != nil {
		t.Errorf("List on repo without ref = %v, %v; want nil, nil",
			entries, err)
	}
	if _, _, _, err := Find(dir, "nope"); err == nil {
		t.Error("Find on repo without ref should error")
	}
}

func TestWriteFromWorktree(t *testing.T) {
	dir := initRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	mustGit(t, dir, "worktree", "add", "-q", "-b", "task/y", wt)

	mustWrite(t, wt, Checkpoint{
		Meta: Meta{SessionID: "wt1", WasaVersion: "test"},
	})
	entries, err := List(dir)
	if err != nil || len(entries) != 1 || entries[0].Meta.SessionID != "wt1" {
		t.Errorf("checkpoint not visible from main repo: %v, %v", entries, err)
	}
}

func TestFindAmbiguousAndMissing(t *testing.T) {
	dir := initRepo(t)
	for _, sid := range []string{"aaa111", "aaa222"} {
		mustWrite(t, dir, Checkpoint{
			Meta: Meta{SessionID: sid, WasaVersion: "test"},
		})
	}
	if _, _, _, err := Find(dir, "aaa"); err == nil {
		t.Error("ambiguous prefix should error")
	}
	if _, _, _, err := Find(dir, "zzz"); err == nil {
		t.Error("missing session should error")
	}
	if e, _, _, err := Find(dir, "aaa111"); err != nil ||
		e.Meta.SessionID != "aaa111" {
		t.Errorf("exact Find = %+v, %v", e.Meta, err)
	}
}

func TestPrune(t *testing.T) {
	dir := initRepo(t)
	branchesBefore := mustGit(t, dir, "for-each-ref", "--format=%(refname)", "refs/heads")
	for _, sid := range []string{"p1", "p2"} {
		mustWrite(t, dir, Checkpoint{
			Meta: Meta{SessionID: sid, WasaVersion: "test"},
		})
	}

	if n, err := Prune(dir, time.Now().Add(-time.Hour)); err != nil || n != 0 {
		t.Errorf("Prune before an hour ago = %d, %v; want 0", n, err)
	}
	if entries, _ := List(dir); len(entries) != 2 {
		t.Fatalf("checkpoints removed by a no-op prune: %v", entries)
	}

	n, err := Prune(dir, time.Now().Add(time.Hour))
	if err != nil || n != 2 {
		t.Errorf("Prune before an hour ahead = %d, %v; want 2", n, err)
	}
	if entries, _ := List(dir); len(entries) != 0 {
		t.Errorf("checkpoints survived prune: %v", entries)
	}
	if got := mustGit(
		t, dir, "for-each-ref", "--format=%(refname)", "refs/heads",
	); got != branchesBefore {
		t.Errorf("prune touched branches: %q -> %q", branchesBefore, got)
	}
}

func TestConcurrentWritesNoContention(t *testing.T) {
	dir := initRepo(t)
	const n = 8
	errs := make(chan error, n)
	for i := range n {
		go func() {
			_, err := Write(dir, Checkpoint{
				Meta: Meta{
					SessionID:   "sess" + string(rune('a'+i)),
					WasaVersion: "test",
				},
			})
			errs <- err
		}()
	}
	for range n {
		if err := <-errs; err != nil {
			t.Errorf("concurrent Write: %v", err)
		}
	}
	if entries, _ := List(dir); len(entries) != n {
		t.Errorf("got %d checkpoints, want %d", len(entries), n)
	}
}

func TestPushWithoutOriginFails(t *testing.T) {
	dir := initRepo(t)
	ref := mustWrite(t, dir, Checkpoint{
		Meta: Meta{SessionID: "s1", WasaVersion: "test"},
	})
	if err := Push(dir, ref); err == nil {
		t.Error("Push without origin should return an error to log")
	}
}

func TestPushToOrigin(t *testing.T) {
	dir := initRepo(t)
	remote := t.TempDir()
	if _, err := gitIn(remote, nil, "init", "-q", "--bare"); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "remote", "add", "origin", remote)
	ref := mustWrite(t, dir, Checkpoint{
		Meta: Meta{SessionID: "s1", WasaVersion: "test"},
	})
	if err := Push(dir, ref); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if out, err := exec.Command(
		"git", "-C", remote, "rev-parse", "--verify", ref,
	).Output(); err != nil || len(out) == 0 {
		t.Errorf("ref not on origin: %v", err)
	}
}
