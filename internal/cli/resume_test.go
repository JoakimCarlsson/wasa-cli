package cli

import (
	"testing"
	"time"

	"github.com/joakimcarlsson/wasa-cli/internal/registry"
)

func TestResolveResumeTargetByBranchPicksNewest(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")

	old := &registry.Session{
		ID:          "old000000000",
		WorkspaceID: ws.ID,
		Branch:      "feature-x",
		Status:      registry.StatusExited,
		CreatedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	recent := &registry.Session{
		ID:          "new000000000",
		WorkspaceID: ws.ID,
		Branch:      "feature-x",
		Status:      registry.StatusExited,
		CreatedAt:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	reg.AddSession(old)
	reg.AddSession(recent)

	got, err := resolveResumeTarget(reg, "feature-x")
	if err != nil {
		t.Fatalf("resolveResumeTarget by branch: %v", err)
	}
	if got.ID != recent.ID {
		t.Errorf("by branch got %s, want newest %s", got.ID, recent.ID)
	}
}

func TestResolveResumeTargetByIDDelegates(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	reg.AddSession(&registry.Session{
		ID:          "abc123def456",
		WorkspaceID: ws.ID,
		Branch:      "some-branch",
	})

	got, err := resolveResumeTarget(reg, "abc123")
	if err != nil {
		t.Fatalf("resolveResumeTarget by id prefix: %v", err)
	}
	if got.ID != "abc123def456" {
		t.Errorf("by id prefix got %s", got.ID)
	}
}

func TestResolveResumeTargetUnknownIsError(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := resolveResumeTarget(reg, "nope"); err == nil {
		t.Fatal("expected error for unknown session/branch query")
	}
}
