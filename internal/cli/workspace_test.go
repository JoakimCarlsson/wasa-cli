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
	wsAdd, created, err := addWorkspace(added, repo, false)
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

	first, created, err := addWorkspace(reg, repo, false)
	if err != nil || !created {
		t.Fatalf("first add = (%+v, %v, %v)", first, created, err)
	}
	second, created, err := addWorkspace(reg, repo, false)
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
	_, _, err = addWorkspace(reg, missing, false)
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

	if _, _, err := addWorkspace(reg, dir, false); err == nil {
		t.Fatal("addWorkspace of a non-git directory returned nil error")
	}
	if got := reg.ListWorkspaces(); len(got) != 0 {
		t.Fatalf("non-git path registered a workspace: %+v", got)
	}
}

// TestAddWorkspaceInitsNonGitDir checks that with doInit set, an existing
// directory that is not a git repository is git-initialized and registered rather
// than rejected, so --init turns a not-yet-versioned project into a workspace.
func TestAddWorkspaceInitsNonGitDir(t *testing.T) {
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

	ws, created, err := addWorkspace(reg, dir, true)
	if err != nil {
		t.Fatalf("addWorkspace --init: %v", err)
	}
	if !created {
		t.Fatal("init of a non-git dir did not report a created workspace")
	}
	if _, _, err := resolveRepo(dir); err != nil {
		t.Fatalf("directory is not a git repository after --init: %v", err)
	}
	if _, ok := reg.Workspace(ws.ID); !ok {
		t.Fatal("initialized repo was not registered")
	}
}

// TestAddWorkspaceInitCreatesMissingPath checks that --init bootstraps a
// brand-new project from a path that does not exist yet: the directory is created,
// git-initialized and registered in one step.
func TestAddWorkspaceInitCreatesMissingPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	missing := filepath.Join(t.TempDir(), "new-project")
	ws, created, err := addWorkspace(reg, missing, true)
	if err != nil {
		t.Fatalf("addWorkspace --init on a missing path: %v", err)
	}
	if !created {
		t.Fatal("init of a new path did not report a created workspace")
	}
	if info, statErr := os.Stat(missing); statErr != nil || !info.IsDir() {
		t.Fatalf("--init did not create the directory: %v", statErr)
	}
	if _, _, err := resolveRepo(missing); err != nil {
		t.Fatalf("created directory is not a git repository: %v", err)
	}
	if _, ok := reg.Workspace(ws.ID); !ok {
		t.Fatal("bootstrapped repo was not registered")
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

func TestResolveWorkspaceByIDPathAndPrefix(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	repo := t.TempDir()
	initGitRepo(t, repo)

	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _, err := addWorkspace(reg, repo, false)
	if err != nil {
		t.Fatalf("addWorkspace: %v", err)
	}

	if got, err := resolveWorkspace(reg, ws.ID); err != nil || got.ID != ws.ID {
		t.Fatalf("resolve by id = (%v, %v), want %s", got, err, ws.ID)
	}
	if got, err := resolveWorkspace(reg, repo); err != nil || got.ID != ws.ID {
		t.Fatalf("resolve by path = (%v, %v), want %s", got, err, ws.ID)
	}
	if got, err := resolveWorkspace(reg, ws.ID[:6]); err != nil ||
		got.ID != ws.ID {
		t.Fatalf("resolve by id prefix = (%v, %v), want %s", got, err, ws.ID)
	}
	if _, err := resolveWorkspace(reg, "nope-not-a-workspace"); err == nil {
		t.Fatal("resolveWorkspace of an unknown query returned nil error")
	}
}

func TestWorkspaceRemoveHelpNotesCascadeAndKeepsRepo(t *testing.T) {
	if err := workspaceRemove([]string{"--help"}); err != nil {
		t.Fatalf("workspaceRemove --help: %v", err)
	}
	lower := strings.ToLower(workspaceRemoveHelp)
	if !strings.Contains(lower, "cascade") {
		t.Fatalf("help does not note the cascade:\n%s", workspaceRemoveHelp)
	}
	if !strings.Contains(lower, "never touched") &&
		!strings.Contains(lower, "never touch") {
		t.Fatalf(
			"help does not reassure the repo on disk is kept:\n%s",
			workspaceRemoveHelp,
		)
	}
}

// TestWorkspaceAddHelpDocumentsInit checks that the help documents both modes:
// the default add of an existing repository and the --init bootstrap that creates
// and git-initializes a new or not-yet-versioned path.
func TestWorkspaceAddHelpDocumentsInit(t *testing.T) {
	if err := workspaceAdd([]string{"--help"}); err != nil {
		t.Fatalf("workspaceAdd --help: %v", err)
	}
	lower := strings.ToLower(workspaceAddHelp)
	if !strings.Contains(lower, "--init") {
		t.Fatalf(
			"help does not document the --init flag:\n%s",
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
