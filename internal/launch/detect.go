package launch

import (
	"os"
	"os/exec"
	"runtime"
)

// KnownAgents is the ordered set of AI coding-agent CLIs wasa probes for on
// PATH. It is the single place to teach wasa a new agent: append the binary
// name and detection follows it. The order is also the presentation order in
// the create-session menu.
var KnownAgents = []string{
	"claude",       // Claude Code
	"codex",        // OpenAI Codex CLI
	"gemini",       // Gemini CLI
	"cursor-agent", // Cursor CLI
	"aider",        // Aider
}

// DetectAgents returns the subset of KnownAgents resolvable on PATH, preserving
// KnownAgents order. Resolution goes through exec.LookPath, so it honors PATHEXT
// on Windows just like the rest of wasa. The result is empty when none of the
// known agents are installed.
func DetectAgents() []string {
	found := make([]string, 0, len(KnownAgents))
	for _, name := range KnownAgents {
		if _, err := exec.LookPath(name); err == nil {
			found = append(found, name)
		}
	}
	return found
}

// Shell returns the OS shell wasa runs for a plain (no-agent) session: $SHELL
// or bash on Unix, %ComSpec% or cmd on Windows. It is the create flow's
// fallback when no known agent is installed and the explicit menu choice for a
// bare shell.
func Shell() string {
	if runtime.GOOS == "windows" {
		if sh := os.Getenv("ComSpec"); sh != "" {
			return sh
		}
		return "cmd"
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "bash"
}
