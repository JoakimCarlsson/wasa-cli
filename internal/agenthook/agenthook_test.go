package agenthook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joakimcarlsson/wasa/internal/hookstatus"
)

func TestCapable(t *testing.T) {
	cases := map[string]bool{
		"claude":          true,
		"/usr/bin/claude": true,
		"claude --resume": true,
		"codex":           false,
		"gemini":          false,
		"bash":            false,
		"":                false,
	}
	for program, want := range cases {
		if got := Capable(program); got != want {
			t.Errorf("Capable(%q) = %v, want %v", program, got, want)
		}
	}
}

func TestMapEvent(t *testing.T) {
	cases := []struct {
		event string
		want  hookstatus.Status
		ok    bool
	}{
		{"SessionStart", hookstatus.StatusWorking, true},
		{"UserPromptSubmit", hookstatus.StatusWorking, true},
		{"Notification", hookstatus.StatusWaiting, true},
		{"Stop", hookstatus.StatusIdle, true},
		{"SubagentStop", hookstatus.StatusIdle, true},
		{"PreCompact", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := MapEvent(c.event)
		if ok != c.ok || got != c.want {
			t.Errorf("MapEvent(%q) = (%q, %v), want (%q, %v)",
				c.event, got, ok, c.want, c.ok)
		}
	}
}

func readHooks(t *testing.T, dir string) map[string][]claudeMatcher {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var top struct {
		Hooks map[string][]claudeMatcher `json:"hooks"`
	}
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("settings is not valid JSON: %v", err)
	}
	return top.Hooks
}

func TestInstallClaudeCreatesHooks(t *testing.T) {
	dir := t.TempDir()
	if err := InstallClaude(dir, "/path/to/wasa hook-handler"); err != nil {
		t.Fatalf("InstallClaude: %v", err)
	}
	hooks := readHooks(t, dir)
	for _, event := range claudeHookEvents {
		if !hasWasaHook(hooks[event]) {
			t.Errorf("event %q missing the wasa hook", event)
		}
	}
}

func TestInstallClaudeIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := InstallClaude(dir, "/path/to/wasa hook-handler"); err != nil {
		t.Fatal(err)
	}
	if err := InstallClaude(dir, "/other/wasa hook-handler"); err != nil {
		t.Fatal(err)
	}
	for event, matchers := range readHooks(t, dir) {
		count := 0
		for _, m := range matchers {
			for _, h := range m.Hooks {
				if strings.Contains(h.Command, hookHandlerMarker) {
					count++
				}
			}
		}
		if count != 1 {
			t.Fatalf("event %q has %d wasa hooks, want exactly 1", event, count)
		}
	}
}

func TestInstallClaudePreservesUserContent(t *testing.T) {
	dir := t.TempDir()
	existing := `{
  "model": "claude-opus-4-8",
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "my-own-formatter"}]}]
  }
}`
	if err := os.WriteFile(
		filepath.Join(dir, "settings.json"),
		[]byte(existing),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := InstallClaude(dir, "/path/to/wasa hook-handler"); err != nil {
		t.Fatalf("InstallClaude: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("settings clobbered into invalid JSON: %v", err)
	}
	if model := string(
		top["model"],
	); !strings.Contains(
		model,
		"claude-opus-4-8",
	) {
		t.Fatalf("unrelated top-level key lost: model = %s", model)
	}

	hooks := readHooks(t, dir)
	var keptUser bool
	for _, m := range hooks["Stop"] {
		for _, h := range m.Hooks {
			if h.Command == "my-own-formatter" {
				keptUser = true
			}
		}
	}
	if !keptUser {
		t.Fatal("user's own Stop hook was dropped")
	}
	if !hasWasaHook(hooks["Stop"]) {
		t.Fatal("wasa hook was not added alongside the user's")
	}
}

func TestInstallClaudeRejectsMalformedSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	bad := []byte("{ this is not json")
	if err := os.WriteFile(path, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InstallClaude(dir, "/path/to/wasa hook-handler"); err == nil {
		t.Fatal("InstallClaude accepted a malformed settings.json")
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(bad) {
		t.Fatalf("malformed settings.json was modified: %s", got)
	}
}
