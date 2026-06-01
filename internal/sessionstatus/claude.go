package sessionstatus

import "path/filepath"

// claudeAdapter integrates Claude Code, whose hooks live in settings.json under
// CLAUDE_CONFIG_DIR (default ~/.claude) and are invoked as commands. This is the
// reference adapter: its config format and event names are well established.
type claudeAdapter struct{}

func (claudeAdapter) Name() string { return "claude" }

func (claudeAdapter) Matches(program string) bool {
	return toolName(program) == "claude"
}

func (claudeAdapter) MapEvent(event string) (Status, bool) {
	switch event {
	case "SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse":
		return Working, true
	case "Notification":
		return Waiting, true
	case "Stop", "SubagentStop":
		return Idle, true
	default:
		return "", false
	}
}

var claudeHookEvents = []string{
	"SessionStart",
	"UserPromptSubmit",
	"Notification",
	"Stop",
	"SubagentStop",
}

func (claudeAdapter) Install(env []string, command string) error {
	dir := configDir(env, "CLAUDE_CONFIG_DIR", ".claude")
	return mergeSettingsHooks(
		filepath.Join(dir, "settings.json"), command, claudeHookEvents,
	)
}
