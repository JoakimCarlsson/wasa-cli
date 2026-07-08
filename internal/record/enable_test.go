package record

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubDetection points the Enable seams at a fixed agent set and a fake wasa
// executable so the shared enable recipe can be exercised without a real agent
// binary on PATH. It restores the originals when the test ends.
func stubDetection(t *testing.T, tools []string) {
	t.Helper()
	origDetect, origExe := detectAgents, executablePath
	t.Cleanup(func() {
		detectAgents = origDetect
		executablePath = origExe
	})
	detectAgents = func() []string { return tools }
	executablePath = func() (string, error) { return "/usr/bin/wasa", nil }
}

// TestEnableInstallsDetectedAgents checks the shared Enable path (used by both
// the CLI and the TUI toggle) installs hooks for every detected agent, reports
// them via InstalledAgents, and adds each settings file to .git/info/exclude —
// the same observable outcome as `wasa record enable`.
func TestEnableInstallsDetectedAgents(t *testing.T) {
	dir := initRepo(t)
	stubDetection(t, []string{"claude", "gemini"})

	got, err := Enable(dir)
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if strings.Join(got, ",") != "claude,gemini" {
		t.Errorf("Enable returned %v, want [claude gemini]", got)
	}
	if installed := InstalledAgents(dir); strings.Join(installed, ",") !=
		"claude,gemini" {
		t.Errorf("InstalledAgents = %v, want [claude gemini]", installed)
	}

	for _, rel := range []string{
		filepath.Join(".claude", "settings.json"),
		filepath.Join(".gemini", "settings.json"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("settings file %s not written: %v", rel, err)
		}
	}

	data, err := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if err != nil {
		t.Fatalf("info/exclude: %v", err)
	}
	for _, entry := range []string{
		".claude/settings.json", ".gemini/settings.json",
	} {
		if !strings.Contains(string(data), entry) {
			t.Errorf("exclude entry %s missing: %q", entry, data)
		}
	}

	if err := RemoveHooks(dir); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if installed := InstalledAgents(dir); len(installed) != 0 {
		t.Errorf("agents still installed after remove: %v", installed)
	}
}

// TestEnableNoDetectedAgents checks Enable is a clean no-op — empty slice, nil
// error, nothing installed — when no supported agent is on PATH, so each caller
// can turn that into its own message rather than an install or a crash.
func TestEnableNoDetectedAgents(t *testing.T) {
	dir := initRepo(t)
	stubDetection(t, nil)

	got, err := Enable(dir)
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Enable returned %v, want empty", got)
	}
	if installed := InstalledAgents(dir); len(installed) != 0 {
		t.Errorf("agents installed with none detected: %v", installed)
	}
}
