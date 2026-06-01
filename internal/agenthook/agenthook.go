// Package agenthook holds the per-tool adapters that connect a hook-emitting
// agent to wasa's hookstatus channel: which programs wasa can receive hooks
// from, how each tool's lifecycle events map onto a hookstatus.Status, and how
// to install the hook into the tool's own configuration so it calls
// `wasa hook-handler`.
//
// Only Claude Code is wired today; Gemini, Codex, OpenCode, Copilot and Cursor
// each have a hook or notify mechanism and slot in here as additional adapters
// behind the same Capable/MapEvent/Install seam. Tools with no hook API (a
// plain shell, an arbitrary program) are simply not Capable, and the cockpit
// keeps deriving their status from the pane heuristic.
package agenthook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joakimcarlsson/wasa/internal/hookstatus"
)

// hookHandlerMarker identifies a hook entry as wasa's, so installing is
// idempotent and never duplicates or disturbs a user's own hooks.
const hookHandlerMarker = "hook-handler"

// Capable reports whether wasa knows how to receive lifecycle hooks from the
// given program. program is the launch token (e.g. "claude" or a path to it);
// only its base name is considered.
func Capable(program string) bool {
	return toolName(program) == "claude"
}

func toolName(program string) string {
	program = strings.TrimSpace(program)
	if program == "" {
		return ""
	}
	fields := strings.Fields(program)
	return filepath.Base(fields[0])
}

// MapEvent maps a Claude Code hook event name onto a status, reporting false for
// an event wasa does not translate (which the handler then ignores). The set is
// kept narrow on purpose: enough to drive working/waiting/idle, not every event
// Claude emits.
//
//	SessionStart, UserPromptSubmit, PreToolUse, PostToolUse -> working
//	Notification                                            -> waiting
//	Stop, SubagentStop                                      -> idle
func MapEvent(event string) (hookstatus.Status, bool) {
	switch event {
	case "SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse":
		return hookstatus.StatusWorking, true
	case "Notification":
		return hookstatus.StatusWaiting, true
	case "Stop", "SubagentStop":
		return hookstatus.StatusIdle, true
	default:
		return "", false
	}
}

// claudeHookEvents are the events wasa subscribes Claude Code to. They bracket a
// turn (SessionStart/UserPromptSubmit → working), catch the need-input moment
// (Notification → waiting) and the turn end (Stop/SubagentStop → idle).
var claudeHookEvents = []string{
	"SessionStart",
	"UserPromptSubmit",
	"Notification",
	"Stop",
	"SubagentStop",
}

type claudeHookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type claudeMatcher struct {
	Matcher string            `json:"matcher,omitempty"`
	Hooks   []claudeHookEntry `json:"hooks"`
}

// InstallClaude merges wasa's status hooks into the Claude Code settings.json
// under configDir, pointing them at command (the `wasa hook-handler` invocation).
// It is additive and idempotent: existing settings and user hooks are preserved
// byte-for-byte except for the wasa entries it adds, and a second call with a
// wasa entry already present is a no-op. A settings.json that exists but is not
// valid JSON is left untouched and reported, so a hand-edited config is never
// clobbered.
func InstallClaude(configDir, command string) error {
	path := filepath.Join(configDir, "settings.json")

	top := map[string]json.RawMessage{}
	if data, err := os.ReadFile(path); err == nil {
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := json.Unmarshal(data, &top); err != nil {
				return fmt.Errorf(
					"claude settings %s is not valid JSON; leaving it untouched: %w",
					path,
					err,
				)
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	hooks := map[string][]claudeMatcher{}
	if raw, ok := top["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return fmt.Errorf(
				"claude settings %s has malformed hooks: %w",
				path,
				err,
			)
		}
	}

	changed := false
	for _, event := range claudeHookEvents {
		if hasWasaHook(hooks[event]) {
			continue
		}
		hooks[event] = append(hooks[event], claudeMatcher{
			Hooks: []claudeHookEntry{{Type: "command", Command: command}},
		})
		changed = true
	}
	if !changed {
		return nil
	}

	rawHooks, err := json.Marshal(hooks)
	if err != nil {
		return err
	}
	top["hooks"] = rawHooks

	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return err
	}
	return atomicWrite(path, out)
}

// hasWasaHook reports whether any matcher already carries a wasa hook entry,
// recognised by the marker in its command, so re-install is a no-op even if the
// wasa binary path changed between launches.
func hasWasaHook(matchers []claudeMatcher) bool {
	for _, m := range matchers {
		for _, h := range m.Hooks {
			if strings.Contains(h.Command, hookHandlerMarker) {
				return true
			}
		}
	}
	return false
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "settings.json.tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
