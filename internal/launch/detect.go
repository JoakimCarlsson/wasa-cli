package launch

import (
	"os"
	"os/exec"
)

// KnownAgents is the ordered set of AI coding-agent CLIs wasa probes for on
// PATH. It is the single place to teach wasa a new agent: append the binary
// name and detection follows it. The order is also the presentation order in
// the create-session menu.
var KnownAgents = []string{
	"claude",       // Claude Code
	"codex",        // OpenAI Codex CLI
	"copilot",      // GitHub Copilot CLI
	"gemini",       // Gemini CLI
	"cursor-agent", // Cursor CLI
	"aider",        // Aider
}

// DetectAgents returns the subset of KnownAgents resolvable on PATH, preserving
// KnownAgents order. The result is empty when none of the known agents are
// installed.
func DetectAgents() []string {
	found := make([]string, 0, len(KnownAgents))
	for _, name := range KnownAgents {
		if _, ok := lookAgent(name); ok {
			found = append(found, name)
		}
	}
	return found
}

// lookAgent resolves an agent CLI on PATH via exec.LookPath, returning its full
// path.
func lookAgent(name string) (string, bool) {
	p, err := exec.LookPath(name)
	if err != nil {
		return "", false
	}
	return p, true
}

// Shell returns the OS shell wasa runs for a plain (no-agent) session: $SHELL or
// bash. It is the create flow's fallback when no known agent is installed and
// the explicit menu choice for a bare shell.
func Shell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "bash"
}
