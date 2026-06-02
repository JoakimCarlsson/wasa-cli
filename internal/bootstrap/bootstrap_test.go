package bootstrap

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// repoAndTree builds a repository directory seeded with files and an empty
// worktree directory, returning both absolute paths.
func repoAndTree(t *testing.T, seed map[string]string) (repo, tree string) {
	t.Helper()
	repo = t.TempDir()
	tree = t.TempDir()
	for rel, content := range seed {
		full := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("seed %q: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("seed %q: %v", rel, err)
		}
	}
	return repo, tree
}

func TestApplyLinkPointsAtRepo(t *testing.T) {
	repo, tree := repoAndTree(t, map[string]string{
		"node_modules/dep/index.js": "module.exports = 1",
	})

	skipped, err := Apply(repo, tree, []string{"node_modules"}, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %v, want none", skipped)
	}

	link := filepath.Join(tree, "node_modules")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	want := filepath.Join(repo, "node_modules")
	if target != want {
		t.Fatalf("symlink target = %q, want the repo copy %q", target, want)
	}
	// The link resolves to the repo's content.
	got, err := os.ReadFile(filepath.Join(link, "dep", "index.js"))
	if err != nil {
		t.Fatalf("read through link: %v", err)
	}
	if string(got) != "module.exports = 1" {
		t.Fatalf("through link = %q, want the repo file content", got)
	}
}

func TestApplyCopyIsIndependent(t *testing.T) {
	repo, tree := repoAndTree(t, map[string]string{
		".env": "TOKEN=original",
	})

	if _, err := Apply(repo, tree, nil, []string{".env"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	worktreeEnv := filepath.Join(tree, ".env")
	if err := os.WriteFile(
		worktreeEnv,
		[]byte("TOKEN=changed"),
		0o644,
	); err != nil {
		t.Fatalf("edit worktree copy: %v", err)
	}

	repoEnv, err := os.ReadFile(filepath.Join(repo, ".env"))
	if err != nil {
		t.Fatalf("read repo .env: %v", err)
	}
	if string(repoEnv) != "TOKEN=original" {
		t.Fatalf(
			"repo .env = %q, want it unchanged by the worktree edit",
			repoEnv,
		)
	}
}

func TestApplyCopyDirectoryRecurses(t *testing.T) {
	repo, tree := repoAndTree(t, map[string]string{
		"config/a.txt":     "a",
		"config/sub/b.txt": "b",
	})

	if _, err := Apply(repo, tree, nil, []string{"config"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	for rel, want := range map[string]string{
		"config/a.txt":     "a",
		"config/sub/b.txt": "b",
	} {
		got, err := os.ReadFile(filepath.Join(tree, rel))
		if err != nil {
			t.Fatalf("read copied %q: %v", rel, err)
		}
		if string(got) != want {
			t.Fatalf("copied %q = %q, want %q", rel, got, want)
		}
	}
}

func TestApplyMissingSourceSkipped(t *testing.T) {
	repo, tree := repoAndTree(t, map[string]string{".env": "X=1"})

	skipped, err := Apply(
		repo, tree,
		[]string{"node_modules"},   // missing
		[]string{".env", "absent"}, // .env present, absent missing
	)
	if err != nil {
		t.Fatalf("Apply with missing sources returned error: %v", err)
	}

	slices.Sort(skipped)
	want := []string{"absent", "node_modules"}
	if !slices.Equal(skipped, want) {
		t.Fatalf("skipped = %v, want %v", skipped, want)
	}

	// The present source was still applied, and the missing ones left nothing.
	if _, err := os.Stat(filepath.Join(tree, ".env")); err != nil {
		t.Fatalf("present source .env was not copied: %v", err)
	}
	_, statErr := os.Lstat(filepath.Join(tree, "node_modules"))
	if !os.IsNotExist(statErr) {
		t.Fatalf("missing link source created something: %v", statErr)
	}
}

func TestApplyEmptyIsNoOp(t *testing.T) {
	repo, tree := repoAndTree(t, nil)

	skipped, err := Apply(repo, tree, nil, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %v, want none", skipped)
	}

	entries, err := os.ReadDir(tree)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("worktree got %d entries, want it untouched", len(entries))
	}
}

func TestFreePortDistinct(t *testing.T) {
	// The kernel rotates ephemeral ports across successive :0 binds, so a small
	// sample reliably contains more than one distinct value; checking the set
	// avoids depending on any two specific calls differing.
	seen := map[int]bool{}
	for range 5 {
		p, err := FreePort()
		if err != nil {
			t.Fatalf("FreePort: %v", err)
		}
		if p <= 0 {
			t.Fatalf("FreePort returned %d, want a positive port", p)
		}
		seen[p] = true
	}
	if len(seen) < 2 {
		t.Fatalf(
			"FreePort returned only %d distinct port(s) over 5 calls",
			len(seen),
		)
	}
}
