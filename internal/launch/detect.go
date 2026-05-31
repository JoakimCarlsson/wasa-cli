package launch

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// lookAgent resolves an agent CLI on PATH, returning its full path. It first
// tries exec.LookPath, which honors PATHEXT (.exe/.cmd/.bat). On Windows it then
// falls back to a PowerShell-script (.ps1) shim: PATHEXT omits .ps1, but npm and
// some editor installs ship CLIs such as copilot as a .ps1.
func lookAgent(name string) (string, bool) {
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	if runtime.GOOS == "windows" {
		return lookPS1(name)
	}
	return "", false
}

// lookPS1 searches PATH for name+".ps1", the shim extension exec.LookPath skips
// because PATHEXT does not list it.
func lookPS1(name string) (string, bool) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		cand := filepath.Join(dir, name+".ps1")
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return cand, true
		}
	}
	return "", false
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
