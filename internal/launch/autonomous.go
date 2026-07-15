package launch

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/joakimcarlsson/wasa-cli/internal/agent"
)

// baseExe reduces a launch program ("/usr/bin/claude --resume") to its base
// executable name ("claude"). It mirrors sessionstatus.toolName so the program
// picker and the agent registry agree on "the agent" even when the field
// already carries flags.
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
	a, ok := agent.ByExe(baseExe(program))
	if !ok || a.Autonomy == nil {
		return "", false
	}
	return a.Autonomy.Flag, true
}

// AutonomousAvailable reports whether program maps to a known autonomous flag. It
// drives the create form's toggle: enabled only when a flag exists to append.
func AutonomousAvailable(program string) bool {
	a, ok := agent.ByExe(baseExe(program))
	return ok && a.Autonomy != nil
}

// WithAutonomous returns program with its agent's skip-permissions flag appended.
// It returns program unchanged when no flag is known for the agent, or when the
// program already carries the canonical flag or one of its aliases — so toggling
// autonomous on is idempotent and never stacks conflicting flags onto a command
// the user already wrote by hand.
func WithAutonomous(program string) string {
	a, ok := agent.ByExe(baseExe(program))
	if !ok || a.Autonomy == nil {
		return program
	}
	tokens := strings.Fields(program)
	if slices.Contains(tokens, a.Autonomy.Flag) {
		return program
	}
	for _, alias := range a.Autonomy.Aliases {
		if slices.Contains(tokens, alias) {
			return program
		}
	}
	return strings.TrimSpace(program) + " " + a.Autonomy.Flag
}
