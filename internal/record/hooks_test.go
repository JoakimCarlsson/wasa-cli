package record

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func claudeSettingsPath(dir string) string {
	return filepath.Join(dir, ".claude", "settings.json")
}

func readSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("settings invalid JSON: %v", err)
	}
	return m
}

// agentConfigPaths maps each supported tool to the hook config file its
// installer owns.
var agentConfigPaths = map[string]string{
	"claude":  filepath.Join(".claude", "settings.json"),
	"gemini":  filepath.Join(".gemini", "settings.json"),
	"codex":   filepath.Join(".codex", "hooks.json"),
	"copilot": filepath.Join(".github", "hooks", "wasa.json"),
	"cursor":  filepath.Join(".cursor", "hooks.json"),
}

func TestInstallRemoveStatusAllAgents(t *testing.T) {
	for tool, rel := range agentConfigPaths {
		t.Run(tool, func(t *testing.T) {
			dir := initRepo(t)
			path := filepath.Join(dir, rel)

			if got := InstalledAgents(dir); len(got) != 0 {
				t.Fatalf("agents installed before install: %v", got)
			}
			if err := InstallHooks(dir, tool, "/usr/bin/wasa"); err != nil {
				t.Fatalf("install: %v", err)
			}
			got := InstalledAgents(dir)
			if len(got) != 1 || got[0] != tool {
				t.Errorf("InstalledAgents = %v, want [%s]", got, tool)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("config not written: %v", err)
			}
			if !strings.Contains(string(raw), hookMarker) {
				t.Errorf("config lacks marker: %s", raw)
			}
			if !strings.Contains(string(raw), "--event end") &&
				tool != "codex" {
				t.Errorf("%s config lacks an end-marked hook: %s", tool, raw)
			}

			if err := InstallHooks(dir, tool, "/usr/bin/wasa"); err != nil {
				t.Fatalf("re-install: %v", err)
			}
			again, _ := os.ReadFile(path)
			if string(raw) != string(again) {
				t.Error("re-install changed config; not idempotent")
			}

			if status := mustGit(
				t, dir, "status", "--porcelain",
			); status != "" {
				t.Errorf("install dirtied git status: %q", status)
			}

			if err := RemoveHooks(dir); err != nil {
				t.Fatalf("remove: %v", err)
			}
			if got := InstalledAgents(dir); len(got) != 0 {
				t.Errorf("agents still installed after remove: %v", got)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Error("config file not deleted when only wasa hooks remained")
			}
		})
	}
}

func TestGeminiInstallEnablesHooksConfig(t *testing.T) {
	dir := initRepo(t)
	if err := InstallHooks(dir, "gemini", "wasa"); err != nil {
		t.Fatal(err)
	}
	m := readSettings(t, filepath.Join(dir, ".gemini", "settings.json"))
	cfg, _ := m["hooksConfig"].(map[string]any)
	if cfg["enabled"] != true {
		t.Errorf("hooksConfig = %v, want enabled true", m["hooksConfig"])
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ".gemini", "settings.json"))
	if !strings.Contains(string(raw), `"name"`) {
		t.Error("gemini hook entries lack the required name field")
	}
}

func TestCodexInstallWritesFeatureFlag(t *testing.T) {
	dir := initRepo(t)
	if err := InstallHooks(dir, "codex", "wasa"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".codex", "config.toml"))
	if err != nil || !strings.Contains(string(data), "hooks = true") {
		t.Errorf("config.toml feature flag missing: %v %q", err, data)
	}
	if err := RemoveHooks(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(
		filepath.Join(dir, ".codex", "config.toml"),
	); !os.IsNotExist(err) {
		t.Error("wasa-written config.toml not removed")
	}
}

func TestCodexInstallRespectsForeignConfigToml(t *testing.T) {
	dir := initRepo(t)
	path := filepath.Join(dir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		path, []byte("model = \"o3\"\n"), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := InstallHooks(dir, "codex", "wasa"); err == nil {
		t.Error("install should surface the missing hooks feature flag")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "model = \"o3\"\n" {
		t.Error("foreign config.toml was modified")
	}
	if err := RemoveHooks(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("foreign config.toml was removed")
	}
}

func TestFlatInstallRefusesForeignFile(t *testing.T) {
	dir := initRepo(t)
	path := filepath.Join(dir, ".cursor", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		path, []byte(`{"version":1,"hooks":{}}`), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := InstallHooks(dir, "cursor", "wasa"); err == nil {
		t.Error("install should refuse a foreign hooks.json")
	}
	if err := RemoveHooks(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("foreign hooks.json was removed")
	}
}

func TestInstallPreservesUserSettings(t *testing.T) {
	dir := initRepo(t)
	path := claudeSettingsPath(dir)
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

	if err := InstallHooks(dir, "claude", "wasa"); err != nil {
		t.Fatalf("install: %v", err)
	}
	m := readSettings(t, path)
	if m["model"] != "opus" {
		t.Error("user setting lost on install")
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "my-linter") {
		t.Error("user hook lost on install")
	}

	if err := RemoveHooks(dir); err != nil {
		t.Fatalf("remove: %v", err)
	}
	raw, _ = os.ReadFile(path)
	if !strings.Contains(string(raw), "my-linter") {
		t.Error("user hook lost on remove")
	}
	if strings.Contains(string(raw), hookMarker) {
		t.Error("wasa hook survived remove")
	}
	m = readSettings(t, path)
	if m["model"] != "opus" {
		t.Error("user setting lost on remove")
	}
}

func TestInstallRefusesInvalidJSON(t *testing.T) {
	dir := initRepo(t)
	path := claudeSettingsPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InstallHooks(dir, "claude", "wasa"); err == nil {
		t.Error("install should refuse to clobber invalid JSON")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "{broken" {
		t.Error("invalid settings were modified")
	}
}

func TestRemoveWithoutSettingsIsNoOp(t *testing.T) {
	if err := RemoveHooks(initRepo(t)); err != nil {
		t.Errorf("remove with no settings: %v", err)
	}
}

func TestEnsureExcluded(t *testing.T) {
	dir := initRepo(t)
	for _, tool := range []string{"claude", "gemini"} {
		if err := InstallHooks(dir, tool, "wasa"); err != nil {
			t.Fatal(err)
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

	if err := InstallHooks(dir, "claude", "wasa"); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if strings.Count(string(again), ".claude/settings.json") != 1 {
		t.Errorf("exclude entry duplicated: %q", again)
	}
}

func TestAgentForProgram(t *testing.T) {
	cases := map[string]string{
		"claude":                         "claude",
		"/usr/bin/claude --resume":       "claude",
		"gemini --yolo":                  "gemini",
		"codex":                          "codex",
		"copilot":                        "copilot",
		"cursor-agent --force":           "cursor",
		"bash":                           "",
		"/opt/homebrew/bin/cursor-agent": "cursor",
	}
	for program, want := range cases {
		got, ok := AgentForProgram(program)
		if want == "" {
			if ok {
				t.Errorf("AgentForProgram(%q) = %q, want none", program, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("AgentForProgram(%q) = %q %v, want %q",
				program, got, ok, want)
		}
	}
}
