package record

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

func TestWriteAndReadBack(t *testing.T) {
	dir := initRepo(t)
	statusBefore := mustGit(t, dir, "status", "--porcelain")

	transcript := `{"type":"user","message":{"role":"user",` +
		`"content":"use ghp_abcdefghijklmnopqrstuvwxyz0123456789 please"}}`
	err := Write(dir, Checkpoint{
		Meta: Meta{
			SessionID:   "abc123",
			Branch:      "task/x",
			WasaVersion: "test",
		},
		Intent: "do the thing with " +
			"ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		Transcript: []byte(transcript),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	err = Write(dir, Checkpoint{
		Meta: Meta{
			SessionID: "abc123", Branch: "task/x",
			Commits: []string{"deadbeef"}, WasaVersion: "test",
		},
		Intent:     "do the thing",
		Transcript: []byte(transcript),
	})
	if err != nil {
		t.Fatalf("second Write: %v", err)
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

	subjects := mustGit(t, dir, "log", "--format=%s", RefName)
	if want := "abc123\nabc123"; subjects != want {
		t.Errorf("ref log subjects = %q, want %q", subjects, want)
	}
	intent := mustGit(t, dir, "show", RefName+":intent.md")
	if intent != "do the thing" {
		t.Errorf("intent.md = %q", intent)
	}
	firstIntent := mustGit(t, dir, "show", RefName+"~1:intent.md")
	if strings.Contains(firstIntent, "ghp_") {
		t.Errorf("unredacted token in intent.md: %q", firstIntent)
	}
	stored := mustGit(t, dir, "show", RefName+":transcript.jsonl")
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
		t.Fatalf("List returned %d entries, want 1", len(entries))
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
		t.Fatalf("Find: %v", err)
	}
	if found.Meta.SessionID != "abc123" || gotIntent != "do the thing" {
		t.Errorf("Find = %+v intent %q", found.Meta, gotIntent)
	}
	if strings.Contains(string(gotTranscript), "ghp_") {
		t.Errorf("Find returned unredacted transcript")
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

	if err := Write(wt, Checkpoint{
		Meta: Meta{SessionID: "wt1", WasaVersion: "test"},
	}); err != nil {
		t.Fatalf("Write from worktree: %v", err)
	}
	if subj := mustGit(t, dir, "log", "--format=%s", RefName); subj != "wt1" {
		t.Errorf("checkpoint not visible from main repo: %q", subj)
	}
}

func TestFindAmbiguousAndMissing(t *testing.T) {
	dir := initRepo(t)
	for _, sid := range []string{"aaa111", "aaa222"} {
		if err := Write(dir, Checkpoint{
			Meta: Meta{SessionID: sid, WasaVersion: "test"},
		}); err != nil {
			t.Fatal(err)
		}
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

func TestPushWithoutOriginFails(t *testing.T) {
	dir := initRepo(t)
	if err := Write(dir, Checkpoint{
		Meta: Meta{SessionID: "s1", WasaVersion: "test"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := Push(dir); err == nil {
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
	if err := Write(dir, Checkpoint{
		Meta: Meta{SessionID: "s1", WasaVersion: "test"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := Push(dir); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if out, err := exec.Command(
		"git", "-C", remote, "rev-parse", "--verify", RefName,
	).Output(); err != nil || len(out) == 0 {
		t.Errorf("ref not on origin: %v", err)
	}
}
