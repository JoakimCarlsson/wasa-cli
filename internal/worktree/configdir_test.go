package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/joakimcarlsson/wasa-cli/internal/agent"
)

// addTrackedDir writes a marker file under dir in repo and commits it, so the
// directory is tracked on the branch a worktree will check out.
func addTrackedDir(t *testing.T, repo, dir string) {
	t.Helper()
	writeMarker(t, repo, dir)
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-m", "add "+dir)
}

// writeMarker creates dir/marker under root with some content.
func writeMarker(t *testing.T, root, dir string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(dir))
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", full, err)
	}
	if err := os.WriteFile(
		filepath.Join(full, "marker"), []byte("x\n"), 0o644,
	); err != nil {
		t.Fatalf("write marker in %q: %v", full, err)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func dirPresent(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// TestProjectConfigStatesCarried asserts a config dir tracked on the branch is
// classified ConfigCarried and physically checked into the worktree by git.
func TestProjectConfigStatesCarried(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	home, repo := t.TempDir(), t.TempDir()
	initRepo(t, repo)
	addTrackedDir(t, repo, ".claude")

	m := New(repo, home, "demo")
	wt, err := m.Add("feature/carried")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	states, err := m.ProjectConfigStates(wt, []string{".claude"})
	if err != nil {
		t.Fatalf("ProjectConfigStates: %v", err)
	}
	if states[".claude"] != ConfigCarried {
		t.Fatalf(".claude state = %s, want carried", states[".claude"])
	}
	if !dirPresent(filepath.Join(wt, ".claude")) {
		t.Fatal("tracked .claude not checked into worktree")
	}
}

// TestProjectConfigStatesIsolated asserts an untracked config dir in the primary
// checkout is classified ConfigIsolated and left out of the worktree.
func TestProjectConfigStatesIsolated(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	home, repo := t.TempDir(), t.TempDir()
	initRepo(t, repo)

	m := New(repo, home, "demo")
	wt, err := m.Add("feature/isolated")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	writeMarker(t, repo, ".cursor")

	states, err := m.ProjectConfigStates(wt, []string{".cursor"})
	if err != nil {
		t.Fatalf("ProjectConfigStates: %v", err)
	}
	if states[".cursor"] != ConfigIsolated {
		t.Fatalf(".cursor state = %s, want isolated", states[".cursor"])
	}
	if dirPresent(filepath.Join(wt, ".cursor")) {
		t.Fatal("untracked .cursor leaked into worktree")
	}
}

// TestProjectConfigStatesAbsent asserts a declared dir present in neither the
// branch nor the primary checkout is classified ConfigAbsent.
func TestProjectConfigStatesAbsent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	home, repo := t.TempDir(), t.TempDir()
	initRepo(t, repo)

	m := New(repo, home, "demo")
	wt, err := m.Add("feature/absent")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	states, err := m.ProjectConfigStates(wt, []string{".gemini"})
	if err != nil {
		t.Fatalf("ProjectConfigStates: %v", err)
	}
	if states[".gemini"] != ConfigAbsent {
		t.Fatalf(".gemini state = %s, want absent", states[".gemini"])
	}
}

// TestProjectConfigPolicyPerAgent drives every supported agent's declared
// ProjectConfigDirs through both policy cases: a committed dir is carried by
// git, an untracked dir is isolated. This is the acceptance test that the
// isolate policy holds for each agent in the registry.
func TestProjectConfigPolicyPerAgent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	for _, a := range agent.Agents {
		for _, dir := range a.ProjectConfigDirs {
			t.Run(a.Exe+"/"+dir+"/carried", func(t *testing.T) {
				home, repo := t.TempDir(), t.TempDir()
				initRepo(t, repo)
				addTrackedDir(t, repo, dir)
				m := New(repo, home, "demo")
				wt, err := m.Add("feature/carried")
				if err != nil {
					t.Fatalf("Add: %v", err)
				}
				states, err := m.ProjectConfigStates(wt, []string{dir})
				if err != nil {
					t.Fatalf("ProjectConfigStates: %v", err)
				}
				if states[dir] != ConfigCarried {
					t.Fatalf("%s state = %s, want carried", dir, states[dir])
				}
				if !dirPresent(filepath.Join(wt, filepath.FromSlash(dir))) {
					t.Fatalf("tracked %s not checked into worktree", dir)
				}
			})

			t.Run(a.Exe+"/"+dir+"/isolated", func(t *testing.T) {
				home, repo := t.TempDir(), t.TempDir()
				initRepo(t, repo)
				m := New(repo, home, "demo")
				wt, err := m.Add("feature/isolated")
				if err != nil {
					t.Fatalf("Add: %v", err)
				}
				writeMarker(t, repo, dir)
				states, err := m.ProjectConfigStates(wt, []string{dir})
				if err != nil {
					t.Fatalf("ProjectConfigStates: %v", err)
				}
				if states[dir] != ConfigIsolated {
					t.Fatalf("%s state = %s, want isolated", dir, states[dir])
				}
				if dirPresent(filepath.Join(wt, filepath.FromSlash(dir))) {
					t.Fatalf("untracked %s leaked into worktree", dir)
				}
			})
		}
	}
}
