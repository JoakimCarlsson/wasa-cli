package sessionstatus

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// cursorAdapter integrates the Cursor CLI (cursor-agent), whose hooks live in a
// single ~/.cursor/hooks.json keyed by event. Its CLI hooks cover the lifecycle
// (start/prompt/stop) but not a distinct "needs input" event, so waiting is left
// to the pane heuristic.
//
// Because hooks.json is one shared file and Cursor's schema may evolve, Install
// only writes a fresh file when none exists rather than merging into a config it
// cannot safely parse — a user with an existing hooks.json adds the wasa command
// themselves. This is the conservative, documented choice over a risky merge.
type cursorAdapter struct{}

func (cursorAdapter) Name() string { return "cursor" }

func (cursorAdapter) Matches(program string) bool {
	switch toolName(program) {
	case "cursor-agent", "cursor":
		return true
	default:
		return false
	}
}

func (cursorAdapter) MapEvent(event string) (Status, bool) {
	switch event {
	case "sessionStart", "beforeSubmitPrompt", "afterFileEdit",
		"beforeShellExecution":
		return Working, true
	case "stop", "sessionEnd":
		return Idle, true
	default:
		return "", false
	}
}

var cursorHookEvents = []string{
	"sessionStart",
	"beforeSubmitPrompt",
	"afterFileEdit",
	"stop",
}

func (cursorAdapter) Install(env []string, command string) error {
	dir := configDir(env, "", ".cursor")
	path := filepath.Join(dir, "hooks.json")
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	hooks := map[string][]map[string]string{}
	for _, event := range cursorHookEvents {
		hooks[event] = []map[string]string{{"command": command}}
	}
	out, err := json.MarshalIndent(
		map[string]any{"version": 1, "hooks": hooks}, "", "  ",
	)
	if err != nil {
		return err
	}
	return atomicWrite(path, out)
}
