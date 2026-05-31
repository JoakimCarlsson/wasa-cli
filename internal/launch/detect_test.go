package launch

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// writeStub creates an executable named name in dir that exec.LookPath will
// resolve: a 0755 file on Unix and a .bat alongside a PATHEXT that lists it on
// Windows.
func writeStub(t *testing.T, dir, name string) {
	t.Helper()
	bin := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		bin += ".bat"
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write stub %s: %v", name, err)
	}
}

func TestDetectAgentsFoundInOrder(t *testing.T) {
	dir := t.TempDir()
	// Install two known agents out of KnownAgents order to prove the result
	// follows the known set rather than the filesystem.
	writeStub(t, dir, "codex")
	writeStub(t, dir, "claude")

	t.Setenv("PATH", dir)
	if runtime.GOOS == "windows" {
		t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	}

	got := DetectAgents()
	want := []string{"claude", "codex"}
	if !slices.Equal(got, want) {
		t.Fatalf("DetectAgents() = %v, want %v", got, want)
	}
}

func TestDetectAgentsNoneFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	}

	if got := DetectAgents(); len(got) != 0 {
		t.Fatalf("DetectAgents() = %v, want empty", got)
	}
}

func TestShellHonorsEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("ComSpec", `C:\Windows\System32\cmd.exe`)
		if got := Shell(); got != `C:\Windows\System32\cmd.exe` {
			t.Fatalf("Shell() = %q, want ComSpec value", got)
		}
		return
	}
	t.Setenv("SHELL", "/usr/bin/zsh")
	if got := Shell(); got != "/usr/bin/zsh" {
		t.Fatalf("Shell() = %q, want $SHELL value", got)
	}
}
