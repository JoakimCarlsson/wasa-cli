package record

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// commit makes a new commit on top of HEAD and returns its full SHA.
func commit(t *testing.T, dir, file, msg string) string {
	t.Helper()
	if err := os.WriteFile(
		filepath.Join(dir, file), []byte(msg+"\n"), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", file)
	mustGit(t, dir, "commit", "-q", "-m", msg)
	return mustGit(t, dir, "rev-parse", "HEAD")
}

func TestExplain(t *testing.T) {
	dir := initRepo(t)
	sha := commit(t, dir, "b.txt", "the change")

	mustWrite(t, dir, Checkpoint{
		Meta: Meta{
			SessionID: "sess1", Branch: "task/y",
			Agent: "claude", Commit: sha, Commits: []string{sha},
			WasaVersion: "test",
		},
		Intent: "why the change exists",
	})
	mustWrite(t, dir, Checkpoint{
		Meta: Meta{
			SessionID: "sess1", Branch: "task/y",
			Agent: "claude", Commits: []string{sha},
			WasaVersion: "test",
		},
		Intent: "why the change exists",
	})

	matches, _, err := Explain(dir, sha, false)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("default returned %d matches, want the newest one", len(matches))
	}
	if matches[0].Meta.SessionID != "sess1" ||
		matches[0].Intent != "why the change exists" {
		t.Errorf("match = %+v intent %q", matches[0].Meta, matches[0].Intent)
	}

	all, _, err := Explain(dir, sha, true)
	if err != nil {
		t.Fatalf("Explain --all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("--all returned %d matches, want both checkpoints", len(all))
	}

	if m, _, err := Explain(dir, sha[:8], false); err != nil || len(m) != 1 {
		t.Errorf("Explain by short SHA = %d, %v", len(m), err)
	}
	if m, _, err := Explain(dir, "HEAD", false); err != nil || len(m) != 1 {
		t.Errorf("Explain HEAD = %d, %v", len(m), err)
	}

	base := mustGit(t, dir, "rev-parse", "HEAD~1")
	m, searched, err := Explain(dir, base, false)
	if err != nil {
		t.Fatalf("Explain base: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("base commit matched %d checkpoints, want 0", len(m))
	}
	if searched != 2 {
		t.Errorf("scanned %d checkpoints, want all 2", searched)
	}

	if _, _, err := Explain(dir, "nope-not-a-commit", false); err == nil {
		t.Error("Explain on an unresolvable commit-ish should error")
	}
}

func TestExplainWithoutRef(t *testing.T) {
	dir := initRepo(t)
	_, _, err := Explain(dir, "HEAD", false)
	if !errors.Is(err, ErrNoRecord) {
		t.Errorf("Explain without a record = %v, want ErrNoRecord", err)
	}
}
