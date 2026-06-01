package sessionstatus

import (
	"os"
	"path/filepath"
)

// opencodeAdapter integrates OpenCode, whose extensibility is a plugin system:
// a JS file in the plugin directory receives events such as session.idle (turn
// finished) and permission.asked (needs input). Install drops a wasa-owned
// plugin file that pipes the event name into `wasa hook-handler`; because it is
// a separate file in the plugin directory it never touches the user's own
// plugins or config.
type opencodeAdapter struct{}

func (opencodeAdapter) Name() string { return "opencode" }

func (opencodeAdapter) Matches(program string) bool {
	return toolName(program) == "opencode"
}

func (opencodeAdapter) MapEvent(event string) (Status, bool) {
	switch event {
	case "permission.asked":
		return Waiting, true
	case "session.idle":
		return Idle, true
	case "session.created", "message.updated", "message.part.updated",
		"tool.execute.before", "permission.replied":
		return Working, true
	default:
		return "", false
	}
}

func (opencodeAdapter) Install(env []string, command string) error {
	base := envValue(env, "XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		base = filepath.Join(home, ".config")
	}
	path := filepath.Join(base, "opencode", "plugin", "wasa-status.js")
	return atomicWrite(path, []byte(opencodePlugin(command)))
}

// opencodePlugin is the plugin source: on each event it pipes a hook payload
// naming the event into the wasa handler, so the same handler path serves
// OpenCode as the other agents.
func opencodePlugin(command string) string {
	return `export const WasaStatus = async ({ $ }) => ({
  event: async ({ event }) => {
    const payload = JSON.stringify({ hook_event_name: event.type })
    await $` + "`echo ${payload} | " + command + "`" + `.quiet().nothrow()
  },
})
`
}
