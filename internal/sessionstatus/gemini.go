package sessionstatus

import "path/filepath"

// geminiAdapter integrates the Gemini CLI, which uses the same command-hook
// shape in settings.json as Claude Code but under ~/.gemini and with its own
// event vocabulary: BeforeAgent/AfterAgent bracket a turn and Notification
// signals a need for input.
type geminiAdapter struct{}

func (geminiAdapter) Name() string { return "gemini" }

func (geminiAdapter) Matches(program string) bool {
	return toolName(program) == "gemini"
}

func (geminiAdapter) MapEvent(event string) (Status, bool) {
	switch event {
	case "SessionStart", "BeforeAgent", "BeforeModel", "BeforeTool":
		return Working, true
	case "Notification":
		return Waiting, true
	case "AfterAgent", "SessionEnd":
		return Idle, true
	default:
		return "", false
	}
}

var geminiHookEvents = []string{
	"SessionStart",
	"BeforeAgent",
	"AfterAgent",
	"Notification",
	"SessionEnd",
}

func (geminiAdapter) Install(env []string, command string) error {
	dir := configDir(env, "GEMINI_CONFIG_DIR", ".gemini")
	return mergeSettingsHooks(
		filepath.Join(dir, "settings.json"), command, geminiHookEvents,
	)
}
