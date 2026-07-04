package record

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readSettings(t *testing.T, dir string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(settingsPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("settings.json invalid: %v", err)
	}
	return m
}

func TestInstallRemoveStatus(t *testing.T) {
	dir := initRepo(t)
	cmd := HookCommand("/usr/bin/wasa")

	if HooksInstalled(dir) {
		t.Fatal("hooks reported installed before install")
	}
	if err := InstallClaudeHooks(dir, cmd); err != nil {
		t.Fatalf("install: %v", err)
	}
	if !HooksInstalled(dir) {
		t.Error("hooks not reported installed")
	}

	raw, _ := os.ReadFile(settingsPath(dir))
	for _, event := range hookEvents {
		if !strings.Contains(string(raw), event) {
			t.Errorf("event %s missing from settings", event)
		}
	}

	if err := InstallClaudeHooks(dir, cmd); err != nil {
		t.Fatalf("re-install: %v", err)
	}
	again, _ := os.ReadFile(settingsPath(dir))
	if string(raw) != string(again) {
		t.Error("re-install changed settings; not idempotent")
	}
	if n := strings.Count(string(again), hookMarker); n != len(hookEvents) {
		t.Errorf("marker appears %d times, want %d", n, len(hookEvents))
	}

	if status := mustGit(t, dir, "status", "--porcelain"); status != "" {
		t.Errorf("settings install dirtied git status: %q", status)
	}

	if err := RemoveClaudeHooks(dir); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if HooksInstalled(dir) {
		t.Error("hooks still reported installed after remove")
	}
	if _, err := os.Stat(settingsPath(dir)); !os.IsNotExist(err) {
		t.Error("settings file not deleted when only wasa hooks remained")
	}
}

func TestInstallPreservesUserSettings(t *testing.T) {
	dir := initRepo(t)
	path := settingsPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	user := `{
  "model": "opus",
  "hooks": {
    "PostToolUse": [
      {"hooks": [{"type": "command", "command": "my-linter"}]}
    ]
  }
}`
	if err := os.WriteFile(path, []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := InstallClaudeHooks(dir, HookCommand("wasa")); err != nil {
		t.Fatalf("install: %v", err)
	}
	m := readSettings(t, dir)
	if m["model"] != "opus" {
		t.Error("user setting lost on install")
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "my-linter") {
		t.Error("user hook lost on install")
	}

	if err := RemoveClaudeHooks(dir); err != nil {
		t.Fatalf("remove: %v", err)
	}
	raw, _ = os.ReadFile(path)
	if !strings.Contains(string(raw), "my-linter") {
		t.Error("user hook lost on remove")
	}
	if strings.Contains(string(raw), hookMarker) {
		t.Error("wasa hook survived remove")
	}
	m = readSettings(t, dir)
	if m["model"] != "opus" {
		t.Error("user setting lost on remove")
	}
}

func TestInstallRefusesInvalidJSON(t *testing.T) {
	dir := initRepo(t)
	path := settingsPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InstallClaudeHooks(dir, HookCommand("wasa")); err == nil {
		t.Error("install should refuse to clobber invalid JSON")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "{broken" {
		t.Error("invalid settings were modified")
	}
}

func TestRemoveWithoutSettingsIsNoOp(t *testing.T) {
	if err := RemoveClaudeHooks(t.TempDir()); err != nil {
		t.Errorf("remove with no settings: %v", err)
	}
}

func TestEnsureExcluded(t *testing.T) {
	dir := initRepo(t)
	if err := InstallClaudeHooks(dir, HookCommand("wasa")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if err != nil {
		t.Fatalf("info/exclude: %v", err)
	}
	if !strings.Contains(string(data), excludeEntry) {
		t.Errorf("exclude entry missing: %q", data)
	}

	if err := InstallClaudeHooks(dir, HookCommand("wasa")); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if strings.Count(string(again), excludeEntry) != 1 {
		t.Errorf("exclude entry duplicated: %q", again)
	}
}
