package sessionstatus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestForAndLookup(t *testing.T) {
	cases := map[string]string{
		"claude":          "claude",
		"/usr/bin/gemini": "gemini",
		"codex --resume":  "codex",
		"opencode":        "opencode",
		"copilot":         "copilot",
		"cursor-agent":    "cursor",
		"cursor":          "cursor",
	}
	for program, want := range cases {
		a, ok := For(program)
		if !ok || a.Name() != want {
			t.Errorf("For(%q) = %v/%q, want %q", program, ok, name(a), want)
		}
	}
	if _, ok := For("bash"); ok {
		t.Error("For(bash) matched an adapter; a shell should have none")
	}
	if _, ok := For(""); ok {
		t.Error("For(\"\") matched an adapter")
	}
	if _, ok := Lookup("gemini"); !ok {
		t.Error("Lookup(gemini) found nothing")
	}
	if _, ok := Lookup("nope"); ok {
		t.Error("Lookup(nope) found an adapter")
	}
	if len(All()) != 6 {
		t.Errorf("All() = %d adapters, want 6", len(All()))
	}
}

func name(a Adapter) string {
	if a == nil {
		return ""
	}
	return a.Name()
}

func TestAdapterMapEvent(t *testing.T) {
	cases := []struct {
		tool, event string
		want        Status
		ok          bool
	}{
		{"claude", "Notification", Waiting, true},
		{"claude", "Stop", Idle, true},
		{"claude", "SessionStart", Working, true},
		{"claude", "PreCompact", "", false},
		{"gemini", "AfterAgent", Idle, true},
		{"gemini", "Notification", Waiting, true},
		{"gemini", "BeforeAgent", Working, true},
		{"codex", "approval-requested", Waiting, true},
		{"codex", "agent-turn-complete", Idle, true},
		{"opencode", "permission.asked", Waiting, true},
		{"opencode", "session.idle", Idle, true},
		{"copilot", "notification", Waiting, true},
		{"copilot", "userPromptSubmitted", Working, true},
		{"cursor", "stop", Idle, true},
		{"cursor", "afterFileEdit", Working, true},
	}
	for _, c := range cases {
		a, ok := Lookup(c.tool)
		if !ok {
			t.Fatalf("no adapter %q", c.tool)
		}
		got, gotOK := a.MapEvent(c.event)
		if got != c.want || gotOK != c.ok {
			t.Errorf("%s.MapEvent(%q) = (%q,%v), want (%q,%v)",
				c.tool, c.event, got, gotOK, c.want, c.ok)
		}
	}
}

func readHooks(t *testing.T, path string) map[string][]jsonHookMatcher {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var top struct {
		Hooks map[string][]jsonHookMatcher `json:"hooks"`
	}
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("%s is not valid JSON: %v", path, err)
	}
	return top.Hooks
}

func TestClaudeInstallMergesIdempotentlyAndPreserves(t *testing.T) {
	dir := t.TempDir()
	env := []string{"CLAUDE_CONFIG_DIR=" + dir}
	path := filepath.Join(dir, "settings.json")

	existing := `{"model":"x","hooks":{"Stop":[{"hooks":[{"type":"command","command":"mine"}]}]}}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	a, _ := Lookup("claude")
	if err := a.Install(env, "/p/wasa hook-handler --tool claude"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := a.Install(
		env,
		"/other/wasa hook-handler --tool claude",
	); err != nil {
		t.Fatalf("reinstall: %v", err)
	}

	var top map[string]json.RawMessage
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("clobbered into invalid JSON: %v", err)
	}
	if !strings.Contains(string(top["model"]), "x") {
		t.Fatal("unrelated key 'model' lost")
	}
	hooks := readHooks(t, path)
	var userKept, wasaCount int
	for _, m := range hooks["Stop"] {
		for _, h := range m.Hooks {
			if h.Command == "mine" {
				userKept++
			}
			if strings.Contains(h.Command, hookHandlerMarker) {
				wasaCount++
			}
		}
	}
	if userKept != 1 {
		t.Fatal("user's own Stop hook was dropped")
	}
	if wasaCount != 1 {
		t.Fatalf(
			"Stop has %d wasa hooks, want exactly 1 (idempotent)",
			wasaCount,
		)
	}
}

func TestClaudeInstallRejectsMalformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	bad := []byte("{ not json")
	if err := os.WriteFile(path, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	a, _ := Lookup("claude")
	if err := a.Install(
		[]string{"CLAUDE_CONFIG_DIR=" + dir},
		"x hook-handler",
	); err == nil {
		t.Fatal("install accepted malformed settings.json")
	}
	if got, _ := os.ReadFile(path); string(got) != string(bad) {
		t.Fatal("malformed settings.json was modified")
	}
}

func TestCodexInstallCreatesOnlyWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	env := []string{"CODEX_HOME=" + dir}
	a, _ := Lookup("codex")
	if err := a.Install(env, "wasa hook-handler --tool codex"); err != nil {
		t.Fatalf("install: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatalf("config.toml not created: %v", err)
	}
	if !strings.Contains(string(data), "notify = [") {
		t.Fatalf("config.toml missing notify: %s", data)
	}

	existing := "model = \"o1\"\n"
	if err := os.WriteFile(
		filepath.Join(dir, "config.toml"),
		[]byte(existing),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := a.Install(env, "wasa hook-handler --tool codex"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	if string(got) != existing {
		t.Fatalf("existing config.toml was modified: %s", got)
	}
}

func TestOpencodeInstallDropsPlugin(t *testing.T) {
	dir := t.TempDir()
	a, _ := Lookup("opencode")
	if err := a.Install(
		[]string{"XDG_CONFIG_HOME=" + dir},
		"wasa hook-handler --tool opencode",
	); err != nil {
		t.Fatalf("install: %v", err)
	}
	data, err := os.ReadFile(
		filepath.Join(dir, "opencode", "plugin", "wasa-status.js"),
	)
	if err != nil {
		t.Fatalf("plugin not written: %v", err)
	}
	if !strings.Contains(string(data), "hook-handler --tool opencode") {
		t.Fatalf("plugin missing handler command: %s", data)
	}
}
