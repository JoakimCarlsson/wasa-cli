package record

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// hookEvent is one agent hook the recorder subscribes to. end marks the
// event that closes the session, which the installed command flags with
// --event end so the handler needs no per-agent event vocabulary.
type hookEvent struct {
	name string
	end  bool
}

// agentSpec is one agent's recording integration: how to install, remove and
// detect its repo-level hook configuration, and how to find its transcript
// when a hook payload does not name it. Each supported agent is one entry in
// agents; everything else in the recorder is agent-agnostic.
type agentSpec struct {
	// tool is the --tool value and the agent name recorded in meta.json.
	tool string
	// exe is the base executable that selects this agent at launch.
	exe string
	// install merges the recording hooks into dir's agent configuration.
	install func(dir, wasaExe string) error
	// remove strips the recording hooks from dir's agent configuration.
	remove func(dir string) error
	// installed reports whether dir carries the recording hooks.
	installed func(dir string) bool
	// transcript best-effort locates the agent's transcript for a session
	// when hook payloads did not carry a path. May be nil.
	transcript func(sessionID, repoDir string) string
}

// agents are the supported recording integrations. Event choices per agent:
// a prompt-bearing event early (intent), a per-tool or per-turn event
// (commit detection) and, where the agent offers one, a session-end event
// (closing checkpoint for unmanaged sessions; Codex has none, so unmanaged
// Codex sessions close only through wasa finish).
var agents = []agentSpec{
	{
		tool: "claude",
		exe:  "claude",
		install: func(dir, wasaExe string) error {
			return installNested(
				settingsFile(dir, ".claude", "settings.json"),
				dir, "claude", wasaExe,
				[]hookEvent{
					{name: "UserPromptSubmit"},
					{name: "PostToolUse"},
					{name: "SessionEnd", end: true},
				},
				nil, nil,
			)
		},
		remove: func(dir string) error {
			return removeNested(
				settingsFile(dir, ".claude", "settings.json"), nil,
			)
		},
		installed: func(dir string) bool {
			return nestedInstalled(
				settingsFile(dir, ".claude", "settings.json"),
			)
		},
		transcript: claudeTranscript,
	},
	{
		tool: "gemini",
		exe:  "gemini",
		install: func(dir, wasaExe string) error {
			return installNested(
				settingsFile(dir, ".gemini", "settings.json"),
				dir, "gemini", wasaExe,
				[]hookEvent{
					{name: "BeforeAgent"},
					{name: "AfterAgent"},
					{name: "SessionEnd", end: true},
				},
				geminiEntry, enableGeminiHooks,
			)
		},
		remove: func(dir string) error {
			return removeNested(
				settingsFile(dir, ".gemini", "settings.json"),
				[]string{"hooksConfig"},
			)
		},
		installed: func(dir string) bool {
			return nestedInstalled(
				settingsFile(dir, ".gemini", "settings.json"),
			)
		},
	},
	{
		tool: "codex",
		exe:  "codex",
		install: func(dir, wasaExe string) error {
			if err := ensureCodexFeature(dir); err != nil {
				return err
			}
			return installNested(
				settingsFile(dir, ".codex", "hooks.json"),
				dir, "codex", wasaExe,
				[]hookEvent{
					{name: "UserPromptSubmit"},
					{name: "PostToolUse"},
					{name: "Stop"},
				},
				codexEntry, nil,
			)
		},
		remove: func(dir string) error {
			if err := removeNested(
				settingsFile(dir, ".codex", "hooks.json"), nil,
			); err != nil {
				return err
			}
			removeCodexFeature(dir)
			return nil
		},
		installed: func(dir string) bool {
			return nestedInstalled(settingsFile(dir, ".codex", "hooks.json"))
		},
		transcript: codexTranscript,
	},
	{
		tool: "copilot",
		exe:  "copilot",
		install: func(dir, wasaExe string) error {
			return installFlat(
				settingsFile(
					dir,
					filepath.Join(".github", "hooks"),
					"wasa.json",
				),
				dir,
				"copilot",
				wasaExe,
				[]hookEvent{
					{name: "userPromptSubmitted"},
					{name: "postToolUse"},
					{name: "sessionEnd", end: true},
				},
				copilotEntry,
			)
		},
		remove: func(dir string) error {
			return removeFlat(settingsFile(
				dir, filepath.Join(".github", "hooks"), "wasa.json",
			))
		},
		installed: func(dir string) bool {
			return flatInstalled(settingsFile(
				dir, filepath.Join(".github", "hooks"), "wasa.json",
			))
		},
		transcript: copilotTranscript,
	},
	{
		tool: "cursor",
		exe:  "cursor-agent",
		install: func(dir, wasaExe string) error {
			return installFlat(
				settingsFile(dir, ".cursor", "hooks.json"),
				dir, "cursor", wasaExe,
				[]hookEvent{
					{name: "beforeSubmitPrompt"},
					{name: "stop"},
					{name: "sessionEnd", end: true},
				},
				cursorEntry,
			)
		},
		remove: func(dir string) error {
			return removeFlat(settingsFile(dir, ".cursor", "hooks.json"))
		},
		installed: func(dir string) bool {
			return flatInstalled(settingsFile(dir, ".cursor", "hooks.json"))
		},
		transcript: cursorTranscript,
	},
}

// specFor returns the integration for a --tool value.
func specFor(tool string) (agentSpec, bool) {
	for _, a := range agents {
		if a.tool == tool {
			return a, true
		}
	}
	return agentSpec{}, false
}

// AgentForProgram returns the recording tool name for a launch program
// ("/usr/bin/cursor-agent --foo" → "cursor"), or false when the agent has no
// recording integration.
func AgentForProgram(program string) (string, bool) {
	exe := baseExe(program)
	for _, a := range agents {
		if a.exe == exe {
			return a.tool, true
		}
	}
	return "", false
}

// InstallHooks installs tool's recording hooks into dir, a repository root
// or worktree.
func InstallHooks(dir, tool, wasaExe string) error {
	a, ok := specFor(tool)
	if !ok {
		return nil
	}
	return a.install(dir, wasaExe)
}

// RemoveHooks strips every agent's recording hooks from dir.
func RemoveHooks(dir string) error {
	for _, a := range agents {
		if err := a.remove(dir); err != nil {
			return err
		}
	}
	return nil
}

// InstalledAgents lists the agents whose recording hooks are present in dir.
func InstalledAgents(dir string) []string {
	var tools []string
	for _, a := range agents {
		if a.installed(dir) {
			tools = append(tools, a.tool)
		}
	}
	return tools
}

// DetectedAgents lists the supported agents found on PATH, which is what
// repo-level enable installs for.
func DetectedAgents() []string {
	var tools []string
	for _, a := range agents {
		if _, err := exec.LookPath(a.exe); err == nil {
			tools = append(tools, a.tool)
		}
	}
	return tools
}

// fallbackTranscript best-effort locates a transcript for sessions whose
// hook payloads never carried a path (Cursor and Copilot omit it on some
// events; a Codex payload may null it).
func fallbackTranscript(tool, sessionID, repoDir string) string {
	a, ok := specFor(tool)
	if !ok || a.transcript == nil || sessionID == "" {
		return ""
	}
	return a.transcript(sessionID, repoDir)
}

// claudeTranscript is ~/.claude/projects/<sanitized-repo>/<session>.jsonl.
func claudeTranscript(sessionID, repoDir string) string {
	return existing(filepath.Join(
		agentHome("CLAUDE_CONFIG_DIR", ".claude"),
		"projects", sanitizePath(repoDir), sessionID+".jsonl",
	))
}

// codexTranscript globs the dated session store
// ~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<session>.jsonl.
func codexTranscript(sessionID, _ string) string {
	m, _ := filepath.Glob(filepath.Join(
		agentHome("CODEX_HOME", ".codex"),
		"sessions", "*", "*", "*", "rollout-*-"+sessionID+".jsonl",
	))
	if len(m) == 0 {
		return ""
	}
	return m[len(m)-1]
}

// copilotTranscript is ~/.copilot/session-state/<session>/events.jsonl.
func copilotTranscript(sessionID, _ string) string {
	return existing(filepath.Join(
		agentHome("", ".copilot"),
		"session-state", sessionID, "events.jsonl",
	))
}

// cursorTranscript is ~/.cursor/projects/<sanitized-repo>/agent-transcripts/
// <session>.jsonl (flat, CLI) or <session>/<session>.jsonl (nested, IDE).
func cursorTranscript(sessionID, repoDir string) string {
	base := filepath.Join(
		agentHome("", ".cursor"),
		"projects", sanitizePath(repoDir), "agent-transcripts",
	)
	if p := existing(
		filepath.Join(base, sessionID, sessionID+".jsonl"),
	); p != "" {
		return p
	}
	return existing(filepath.Join(base, sessionID+".jsonl"))
}

// agentHome resolves an agent's data directory: envKey when set (and
// non-empty), else homeSub under the user's home directory.
func agentHome(envKey, homeSub string) string {
	if envKey != "" {
		if v := os.Getenv(envKey); v != "" {
			return v
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return homeSub
	}
	return filepath.Join(home, homeSub)
}

// sanitizePath mirrors the path munging Claude Code and Cursor use for their
// per-project transcript directories: every non-alphanumeric byte becomes
// a dash.
func sanitizePath(p string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		}
		return '-'
	}, p)
}

func existing(path string) string {
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}
