package cli

import (
	"strings"
	"testing"

	"github.com/joakimcarlsson/wasa-cli/internal/registry"
)

func finishTestRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	reg.AddSession(&registry.Session{
		ID:          "abc123def456",
		WorkspaceID: ws.ID,
		Title:       "fix login",
	})
	reg.AddSession(&registry.Session{
		ID:          "fff000aaa111",
		WorkspaceID: ws.ID,
		Title:       "fix login",
	})
	return reg
}

func TestResolveSessionByIDTitleAndPrefix(t *testing.T) {
	reg := finishTestRegistry(t)

	if s, err := resolveSession(reg, "abc123def456"); err != nil ||
		s.ID != "abc123def456" {
		t.Fatalf("resolve by full id = (%+v, %v)", s, err)
	}
	if s, err := resolveSession(reg, "abc123"); err != nil ||
		s.ID != "abc123def456" {
		t.Fatalf("resolve by unique prefix = (%+v, %v)", s, err)
	}
	if s, err := resolveSession(reg, "fff000aaa111"); err != nil ||
		s.ID != "fff000aaa111" {
		t.Fatalf("resolve by id when title is shared = (%+v, %v)", s, err)
	}
}

func TestResolveSessionUnknownIsError(t *testing.T) {
	reg := finishTestRegistry(t)
	if _, err := resolveSession(reg, "nope"); err == nil {
		t.Fatal("resolveSession returned nil error for an unknown query")
	}
}

func TestResolveSessionAmbiguousIsError(t *testing.T) {
	reg := finishTestRegistry(t)
	if _, err := resolveSession(reg, "fix login"); err == nil {
		t.Fatal("resolveSession returned nil error for an ambiguous title")
	}
}

func TestResolveSessionEmptyIsError(t *testing.T) {
	reg := finishTestRegistry(t)
	if _, err := resolveSession(reg, ""); err == nil {
		t.Fatal("resolveSession returned nil error for an empty query")
	}
}

func TestFinishHelpDocumentsNoMerge(t *testing.T) {
	if err := runFinish([]string{"--help"}); err != nil {
		t.Fatalf("runFinish --help: %v", err)
	}
	lower := strings.ToLower(finishHelp)
	if !strings.Contains(lower, "never merges") {
		t.Fatalf("finish help does not state it never merges:\n%s", finishHelp)
	}
	for _, word := range []string{"merge", "rebase", "push", "pull request"} {
		if !strings.Contains(lower, word) {
			t.Fatalf("finish help does not mention %q", word)
		}
	}
}
