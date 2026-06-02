package launch

import (
	"path/filepath"
	"slices"
	"strings"
)

// autonomyFlag is one agent's skip-permissions / "autonomous" configuration: the
// canonical flag wasa appends when the toggle is on, plus aliases — other tokens
// that already imply autonomy, whose presence suppresses the append so wasa never
// double-applies or stacks conflicting flags.
type autonomyFlag struct {
	flag    string
	aliases []string
}

// autonomyFlags maps an agent's base executable to its skip-permissions flag.
// These CLIs move fast; this map is the single place to teach wasa a new agent's
// autonomous flag or adjust an existing one. Keyed by base executable so it
// agrees with KnownAgents and the program picker.
var autonomyFlags = map[string]autonomyFlag{
	"claude": {flag: "--dangerously-skip-permissions"},
	"codex": {
		flag:    "--dangerously-bypass-approvals-and-sandbox",
		aliases: []string{"--yolo", "--full-auto"},
	},
	"gemini": {flag: "--yolo", aliases: []string{"--approval-mode"}},
	"copilot": {
		flag:    "--allow-all-tools",
		aliases: []string{"--allow-all", "--yolo"},
	},
	"cursor-agent": {flag: "--force"},
}

// baseExe reduces a launch program ("/usr/bin/claude --resume") to its base
// executable name ("claude"). It mirrors sessionstatus.toolName so the program
// picker and the flag mapping agree on "the agent" even when the field already
// carries flags.
func baseExe(program string) string {
	program = strings.TrimSpace(program)
	if program == "" {
		return ""
	}
	return filepath.Base(strings.Fields(program)[0])
}

// AutonomousFlag returns the canonical skip-permissions flag for program's base
// executable and whether one is known. A shell or unknown program has no flag.
func AutonomousFlag(program string) (string, bool) {
	a, ok := autonomyFlags[baseExe(program)]
	if !ok {
		return "", false
	}
	return a.flag, true
}

// AutonomousAvailable reports whether program maps to a known autonomous flag. It
// drives the create form's toggle: enabled only when a flag exists to append.
func AutonomousAvailable(program string) bool {
	_, ok := autonomyFlags[baseExe(program)]
	return ok
}

// WithAutonomous returns program with its agent's skip-permissions flag appended.
// It returns program unchanged when no flag is known for the agent, or when the
// program already carries the canonical flag or one of its aliases — so toggling
// autonomous on is idempotent and never stacks conflicting flags onto a command
// the user already wrote by hand.
func WithAutonomous(program string) string {
	a, ok := autonomyFlags[baseExe(program)]
	if !ok {
		return program
	}
	tokens := strings.Fields(program)
	if slices.Contains(tokens, a.flag) {
		return program
	}
	for _, alias := range a.aliases {
		if slices.Contains(tokens, alias) {
			return program
		}
	}
	return strings.TrimSpace(program) + " " + a.flag
}
