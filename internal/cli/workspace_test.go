package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joakimcarlsson/wasa/internal/registry"
)

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	run("commit", "--allow-empty", "-m", "initial")
}

func TestAddWorkspaceMatchesAutoRegistration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	repo := t.TempDir()
	initGitRepo(t, repo)

	added, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	wsAdd, created, err := addWorkspace(added, repo)
	if err != nil {
		t.Fatalf("addWorkspace: %v", err)
	}
	if !created {
		t.Fatal("first addWorkspace did not report created")
	}

	repoPath, remoteURL, err := resolveRepo(repo)
	if err != nil {
		t.Fatalf("resolveRepo: %v", err)
	}
	wantID := registry.WorkspaceID(repoPath, remoteURL)
	if wsAdd.ID != wantID {
		t.Fatalf(
			"workspace id = %q, want auto-registration id %q",
			wsAdd.ID,
			wantID,
		)
	}

	if len(wsAdd.Profiles) != 1 ||
		wsAdd.Profiles[0].Name != registry.DefaultProfileName {
		t.Fatalf("profiles = %+v, want one default profile", wsAdd.Profiles)
	}

	auto, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	wsAuto, _ := registerRepo(auto, repoPath, remoteURL)
	if wsAdd.ID != wsAuto.ID {
		t.Fatalf("add id %q != in-repo registration id %q", wsAdd.ID, wsAuto.ID)
	}
}

func TestAddWorkspaceIsIdempotent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	repo := t.TempDir()
	initGitRepo(t, repo)

	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	first, created, err := addWorkspace(reg, repo)
	if err != nil || !created {
		t.Fatalf("first add = (%+v, %v, %v)", first, created, err)
	}
	second, created, err := addWorkspace(reg, repo)
	if err != nil {
		t.Fatalf("second add: %v", err)
	}
	if created {
		t.Fatal("second add reported created for an already-registered repo")
	}
	if second.ID != first.ID {
		t.Fatalf("second add id = %q, want %q", second.ID, first.ID)
	}
	if got := reg.ListWorkspaces(); len(got) != 1 {
		t.Fatalf("workspace count = %d, want 1 (no duplicate)", len(got))
	}
}

func TestAddWorkspaceMissingPath(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	missing := filepath.Join(t.TempDir(), "nope")
	_, _, err = addWorkspace(reg, missing)
	if err == nil {
		t.Fatal("addWorkspace of a missing path returned nil error")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("error %q does not explain the missing path", err)
	}
	if got := reg.ListWorkspaces(); len(got) != 0 {
		t.Fatalf("missing path registered a workspace: %+v", got)
	}
}

func TestAddWorkspaceNonGitPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "file.txt"), []byte("x"), 0o600,
	); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, _, err := addWorkspace(reg, dir); err == nil {
		t.Fatal("addWorkspace of a non-git directory returned nil error")
	}
	if got := reg.ListWorkspaces(); len(got) != 0 {
		t.Fatalf("non-git path registered a workspace: %+v", got)
	}
}

func TestResolvePlainDirDefaultsToCwd(t *testing.T) {
	got, err := resolvePlainDir("")
	if err != nil {
		t.Fatalf("resolvePlainDir(\"\"): %v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if got != cwd {
		t.Fatalf("resolvePlainDir(\"\") = %q, want cwd %q", got, cwd)
	}
}

func TestResolvePlainDirExplicit(t *testing.T) {
	dir := t.TempDir()
	got, err := resolvePlainDir(dir)
	if err != nil {
		t.Fatalf("resolvePlainDir(%q): %v", dir, err)
	}
	abs, _ := filepath.Abs(dir)
	if got != abs {
		t.Fatalf("resolvePlainDir(%q) = %q, want %q", dir, got, abs)
	}

	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := resolvePlainDir(file); err == nil {
		t.Fatal("resolvePlainDir of a file returned nil error")
	}
	if _, err := resolvePlainDir(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("resolvePlainDir of a missing path returned nil error")
	}
}

func TestWorkspaceForDirAttachesInsideRepoAndNilOutside(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	repo := t.TempDir()
	initGitRepo(t, repo)

	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	ws := workspaceForDir(reg, repo)
	if ws == nil {
		t.Fatal("workspaceForDir returned nil inside a git repository")
	}
	repoPath, remoteURL, err := resolveRepo(repo)
	if err != nil {
		t.Fatalf("resolveRepo: %v", err)
	}
	if ws.ID != registry.WorkspaceID(repoPath, remoteURL) {
		t.Fatalf("workspaceForDir id = %q, want the repo's workspace id", ws.ID)
	}

	nonRepo := t.TempDir()
	if got := workspaceForDir(reg, nonRepo); got != nil {
		t.Fatalf("workspaceForDir outside any repo = %+v, want nil", got)
	}
}

func TestWorkspaceAddHelpNotesDeferral(t *testing.T) {
	if err := workspaceAdd([]string{"--help"}); err != nil {
		t.Fatalf("workspaceAdd --help: %v", err)
	}
	lower := strings.ToLower(workspaceAddHelp)
	if !strings.Contains(lower, "out of scope") {
		t.Fatalf(
			"help does not note the path-bootstrap deferral:\n%s",
			workspaceAddHelp,
		)
	}
	if !strings.Contains(lower, "git init") {
		t.Fatalf(
			"help does not mention git init bootstrap:\n%s",
			workspaceAddHelp,
		)
	}
}
