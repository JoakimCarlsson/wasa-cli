package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/joakimcarlsson/wasa/internal/launch"
)

func stubAgent(t *testing.T, dir, name string) {
	t.Helper()
	bin := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		bin += ".bat"
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write stub %s: %v", name, err)
	}
}

func setPATH(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir)
	if runtime.GOOS == "windows" {
		t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	}
}

func TestResolveProgramSoleAgent(t *testing.T) {
	dir := t.TempDir()
	stubAgent(t, dir, "codex")
	setPATH(t, dir)

	got, err := resolveProgram()
	if err != nil {
		t.Fatalf("resolveProgram() error = %v", err)
	}
	if got != "codex" {
		t.Fatalf("resolveProgram() = %q, want codex", got)
	}
}

func TestResolveProgramMultipleAgentsErrors(t *testing.T) {
	dir := t.TempDir()
	stubAgent(t, dir, "claude")
	stubAgent(t, dir, "codex")
	setPATH(t, dir)

	_, err := resolveProgram()
	if err == nil {
		t.Fatal("resolveProgram() error = nil, want error listing agents")
	}
	for _, name := range []string{"claude", "codex", "--program"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q missing %q", err.Error(), name)
		}
	}
}

func TestResolveProgramNoAgentsFallsBackToShell(t *testing.T) {
	setPATH(t, t.TempDir())

	got, err := resolveProgram()
	if err != nil {
		t.Fatalf("resolveProgram() error = %v", err)
	}
	if got != launch.Shell() {
		t.Fatalf("resolveProgram() = %q, want shell %q", got, launch.Shell())
	}
}
