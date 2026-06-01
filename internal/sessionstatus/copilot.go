package sessionstatus

import (
	"encoding/json"
	"path/filepath"
)

// copilotAdapter integrates the GitHub Copilot CLI, whose hooks are JSON files
// in ~/.copilot/hooks/. Install drops a wasa-owned hook file there, so it never
// touches the user's own hook files. The event vocabulary follows Copilot's
// reference: prompt/tool events mean working, notification means it needs input.
//
// The on-disk schema is Copilot's and may evolve; the file is wasa-owned and
// self-contained, so a schema mismatch degrades to "no hook" (the cockpit falls
// back to the pane heuristic) rather than breaking Copilot.
type copilotAdapter struct{}

func (copilotAdapter) Name() string { return "copilot" }

func (copilotAdapter) Matches(program string) bool {
	return toolName(program) == "copilot"
}

func (copilotAdapter) MapEvent(event string) (Status, bool) {
	switch event {
	case "sessionStart", "userPromptSubmitted", "preToolUse", "postToolUse":
		return Working, true
	case "notification":
		return Waiting, true
	case "sessionEnd", "stop":
		return Idle, true
	default:
		return "", false
	}
}

var copilotHookEvents = []string{
	"sessionStart",
	"userPromptSubmitted",
	"notification",
	"sessionEnd",
}

func (copilotAdapter) Install(env []string, command string) error {
	dir := configDir(env, "", filepath.Join(".copilot", "hooks"))
	hooks := map[string][]map[string]string{}
	for _, event := range copilotHookEvents {
		hooks[event] = []map[string]string{
			{"type": "command", "command": command},
		}
	}
	out, err := json.MarshalIndent(map[string]any{"hooks": hooks}, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, "wasa-status.json"), out)
}
