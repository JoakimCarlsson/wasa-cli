package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joakimcarlsson/wasa-cli/internal/link/gitremote"
	"github.com/joakimcarlsson/wasa-cli/internal/record"
	"github.com/joakimcarlsson/wasa-cli/internal/registry"
)

func TestLinkCommandsRegistered(t *testing.T) {
	for _, name := range []string{"link", "unlink"} {
		if _, ok := lookup(name); !ok {
			t.Fatalf("%s command not registered", name)
		}
	}
}

func TestLinkRejectsExtraArguments(t *testing.T) {
	if err := runLink([]string{"extra"}); err == nil {
		t.Error("runLink with an argument: want a usage error")
	}
	if err := runUnlink([]string{"extra"}); err == nil {
		t.Error("runUnlink with an argument: want a usage error")
	}
}

// TestLinkWithoutALoginFailsWithTheCure is the acceptance criterion verbatim:
// the message names the one command that fixes it.
func TestLinkWithoutALoginFailsWithTheCure(t *testing.T) {
	inGitRepo(t)

	err := runLink(nil)
	if err == nil {
		t.Fatal("runLink without a login: want an error")
	}
	if !strings.Contains(err.Error(), "not logged in") ||
		!strings.Contains(err.Error(), "login --core") {
		t.Errorf("err = %v, want the not-logged-in cure", err)
	}
}

func TestLinkCoreMustMatchTheCurrentContext(t *testing.T) {
	if _, err := linkCore("", "https://core.example"); err != nil {
		t.Errorf("linkCore with no request = %v", err)
	}
	got, err := linkCore("https://core.example/", "https://core.example")
	if err != nil || got != "https://core.example" {
		t.Errorf("linkCore = (%q, %v), want the normalized core", got, err)
	}
	if _, err := linkCore(
		"https://other.example", "https://core.example",
	); err == nil {
		t.Error("linkCore against a core with no login: want an error")
	}
}

func TestRepoNameRejectsAnythingButASlug(t *testing.T) {
	name, err := repoName("acme/widgets.git")
	if err != nil || name != "widgets" {
		t.Errorf("repoName = (%q, %v), want widgets", name, err)
	}
	for _, slug := range []string{"widgets", "", "/widgets", "acme/"} {
		if _, err := repoName(slug); err == nil {
			t.Errorf("repoName(%q): want an error", slug)
		}
	}
}

// TestUnlinkOnAnUnlinkedWorkspaceIsAQuietNoOp keeps unlink safe to run in any
// repository: it reports rather than failing, and touches nothing.
func TestUnlinkOnAnUnlinkedWorkspaceIsAQuietNoOp(t *testing.T) {
	inGitRepo(t)

	if err := runUnlink(nil); err != nil {
		t.Fatalf("runUnlink on an unlinked workspace: %v", err)
	}
}

// TestUnlinkDropsTheRecordAndTheRemote covers the whole reversal: the link
// record goes, the remote goes with it, and sync falls back to origin.
func TestUnlinkDropsTheRecordAndTheRemote(t *testing.T) {
	repoDir := inGitRepo(t)
	home := os.Getenv("WASA_HOME")

	reg, err := registry.Open(home)
	if err != nil {
		t.Fatalf("registry.Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace(repoDir, "", "fixture")
	ws.Link = &registry.Link{
		CoreURL: "https://core.example",
		RepoID:  "01J000000000000000000000AB",
		Slug:    "acme/widgets",
	}
	if err := reg.Save(); err != nil {
		t.Fatalf("registry.Save: %v", err)
	}
	if err := gitremote.Configure(
		repoDir, gitremote.RemoteName, "acme/widgets", "https://core.example",
	); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if got := syncRemote(repoDir, ""); got != record.LinkedRemote {
		t.Fatalf("syncRemote while linked = %q, want %q",
			got, record.LinkedRemote)
	}

	if err := runUnlink(nil); err != nil {
		t.Fatalf("runUnlink: %v", err)
	}

	reg, err = registry.Open(home)
	if err != nil {
		t.Fatalf("registry.Open: %v", err)
	}
	after, ok := reg.Workspace(registry.WorkspaceID(repoDir, ""))
	if !ok || after.Link != nil {
		t.Errorf("workspace after unlink = %+v, want no link", after)
	}
	if got := gitremote.ConfiguredCore(
		repoDir, gitremote.RemoteName,
	); got != "" {
		t.Errorf("pinned core after unlink = %q, want empty", got)
	}
	if got := syncRemote(repoDir, ""); got != "origin" {
		t.Errorf("syncRemote after unlink = %q, want origin", got)
	}
}

// TestSyncRemoteHonoursAnExplicitRemote keeps a remote the user typed from
// being rerouted through the control plane behind their back.
func TestSyncRemoteHonoursAnExplicitRemote(t *testing.T) {
	if got := syncRemote(t.TempDir(), "upstream"); got != "upstream" {
		t.Errorf("syncRemote = %q, want upstream", got)
	}
}

// TestHelperCorePrefersThePinnedCore is the regression the link record exists
// to prevent: a transfer follows the core its remote names, not whichever
// context happens to be current.
func TestHelperCorePrefersThePinnedCore(t *testing.T) {
	repoDir := inGitRepo(t)
	if err := gitremote.Configure(
		repoDir, gitremote.RemoteName, "acme/widgets", "https://pinned.example",
	); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	got, err := helperCore(gitremote.RemoteName)
	if err != nil {
		t.Fatalf("helperCore: %v", err)
	}
	if got != "https://pinned.example" {
		t.Errorf("helperCore = %q, want the pinned core", got)
	}
}

// TestHelperCoreFallsBackToTheContext keeps a hand-typed wasa:// remote
// working: with no pin there is nothing to follow but the current login, and
// with no login the existing cure stands.
func TestHelperCoreFallsBackToTheContext(t *testing.T) {
	inGitRepo(t)

	if _, err := helperCore("origin"); err == nil ||
		!strings.Contains(err.Error(), "not logged in") {
		t.Errorf("helperCore without a pin or a login = %v", err)
	}
}

// inGitRepo puts the test in a fresh git repository with its own $WASA_HOME
// and config/cache dirs, so nothing it does reaches the developer's own.
func inGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	out, err := exec.Command("git", "-C", dir, "init").CombinedOutput()
	if err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	t.Setenv("WASA_HOME", filepath.Join(t.TempDir(), "home"))
	t.Chdir(dir)
	return dir
}
