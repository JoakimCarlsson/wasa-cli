package sessionstatus

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// hookHandlerMarker identifies a hook entry as wasa's, so installing is
// idempotent and never duplicates or disturbs a user's own hooks.
const hookHandlerMarker = "hook-handler"

type jsonHookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type jsonHookMatcher struct {
	Matcher string          `json:"matcher,omitempty"`
	Hooks   []jsonHookEntry `json:"hooks"`
}

// mergeSettingsHooks merges wasa's command hook into the settings.json at path,
// for each event named, in the shape Claude Code and Gemini CLI both use:
//
//	{"hooks": {"<Event>": [{"hooks": [{"type": "command", "command": "<cmd>"}]}]}}
//
// It is additive and idempotent: existing settings and user hooks are preserved
// except for the wasa entries it adds, and a re-install (even with a different
// command path) is a no-op once a wasa entry is present. A settings.json that
// exists but is not valid JSON is left untouched and the error returned, so a
// hand-edited config is never clobbered.
func mergeSettingsHooks(path, command string, events []string) error {
	top := map[string]json.RawMessage{}
	if data, err := os.ReadFile(path); err == nil {
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := json.Unmarshal(data, &top); err != nil {
				return fmt.Errorf(
					"settings %s is not valid JSON; leaving it untouched: %w",
					path, err,
				)
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	hooks := map[string][]jsonHookMatcher{}
	if raw, ok := top["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return fmt.Errorf("settings %s has malformed hooks: %w", path, err)
		}
	}

	changed := false
	for _, event := range events {
		if hasWasaHook(hooks[event]) {
			continue
		}
		hooks[event] = append(hooks[event], jsonHookMatcher{
			Hooks: []jsonHookEntry{{Type: "command", Command: command}},
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
	return atomicWrite(path, out)
}

func hasWasaHook(matchers []jsonHookMatcher) bool {
	for _, m := range matchers {
		for _, h := range m.Hooks {
			if strings.Contains(h.Command, hookHandlerMarker) {
				return true
			}
		}
	}
	return false
}
