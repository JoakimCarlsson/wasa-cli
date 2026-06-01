package sessionstatus

import (
	"os"
	"path/filepath"
	"strings"
)

// codexAdapter integrates the OpenAI Codex CLI. Codex calls a single `notify`
// program (configured in ~/.codex/config.toml) with a JSON payload whose "type"
// is the event — approval-requested when it needs input, agent-turn-complete
// when a turn ends. Codex emits no "working" event, so working is left to the
// pane heuristic between turns.
//
// Install is conservative on purpose: config.toml is TOML and wasa carries no
// TOML parser, so rather than risk corrupting an existing hand-tuned config it
// only writes a fresh file when none exists. A user with an existing config.toml
// must add the notify line themselves; this is documented rather than guessed.
type codexAdapter struct{}

func (codexAdapter) Name() string { return "codex" }

func (codexAdapter) Matches(program string) bool {
	return toolName(program) == "codex"
}

func (codexAdapter) MapEvent(event string) (Status, bool) {
	switch event {
	case "approval-requested":
		return Waiting, true
	case "agent-turn-complete":
		return Idle, true
	default:
		return "", false
	}
}

func (codexAdapter) Install(env []string, command string) error {
	dir := configDir(env, "CODEX_HOME", ".codex")
	path := filepath.Join(dir, "config.toml")
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	content := "notify = [" + tomlArgs(command) + "]\n" +
		"\n[tui]\nnotifications = [\"agent-turn-complete\", \"approval-requested\"]\n"
	return atomicWrite(path, []byte(content))
}

// tomlArgs renders a space-separated command as a TOML array body of quoted
// arguments: `wasa hook-handler --tool codex` becomes
// `"wasa", "hook-handler", "--tool", "codex"`.
func tomlArgs(command string) string {
	fields := strings.Fields(command)
	quoted := make([]string, len(fields))
	for i, f := range fields {
		quoted[i] = "\"" + strings.ReplaceAll(f, "\"", "\\\"") + "\""
	}
	return strings.Join(quoted, ", ")
}
