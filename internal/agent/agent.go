package agent

import "slices"

// Autonomy is an agent's skip-permissions / "autonomous" configuration: the
// canonical flag wasa appends when the toggle is on, plus aliases — other
// tokens that already imply autonomy, whose presence suppresses the append so
// wasa never double-applies or stacks conflicting flags.
type Autonomy struct {
	Flag    string
	Aliases []string
}

// Agent is one CLI's declared capabilities across every seam that needs to
// know about it. A capability an agent legitimately lacks is declared as its
// zero value (empty string / nil), a visible "not supported" rather than an
// accidental omission from a separate map.
type Agent struct {
	// Exe is the base executable name wasa probes on PATH and launches
	// ("claude", "cursor-agent"). It is the launch-time key and must be
	// unique and non-empty.
	Exe string

	// ConfigDirVar is the environment variable that overrides this agent's
	// config/account directory, or "" when the agent has no known
	// convention.
	ConfigDirVar string
	// ConfigDirAliases are additional program names that share ConfigDirVar
	// without being a launchable agent in their own right (e.g. "gh" shares
	// copilot's GH_CONFIG_DIR).
	ConfigDirAliases []string

	// Autonomy is the agent's skip-permissions flag, or nil when the agent
	// has no such flag.
	Autonomy *Autonomy

	// RecorderTool is the recording tool name this agent binds to — the
	// value a record.Recorder implementation returns from Tool() — or "" when
	// the agent has no recording integration. It is deliberately distinct
	// from Exe: cursor-agent's launch executable is "cursor-agent" but its
	// recorder's Tool() is "cursor".
	RecorderTool string
}

// Agents is the single, ordered declaration of every agent wasa knows about.
// The order is the presentation order in the create-session menu and the
// detection order on PATH; appending an agent here is the one place to teach
// wasa about it across launch, profile and record.
var Agents = []Agent{
	{
		Exe:          "claude",
		ConfigDirVar: "CLAUDE_CONFIG_DIR",
		Autonomy:     &Autonomy{Flag: "--dangerously-skip-permissions"},
		RecorderTool: "claude",
	},
	{
		// CODEX_HOME is the env var Codex's own CLI reads for its config/data
		// directory (default ~/.codex); record/codex.go already resolves a
		// session's transcript through it via agentHome.
		Exe:          "codex",
		ConfigDirVar: "CODEX_HOME",
		Autonomy: &Autonomy{
			Flag:    "--dangerously-bypass-approvals-and-sandbox",
			Aliases: []string{"--yolo", "--full-auto"},
		},
		RecorderTool: "codex",
	},
	{
		Exe:              "copilot",
		ConfigDirVar:     "GH_CONFIG_DIR",
		ConfigDirAliases: []string{"gh"},
		Autonomy: &Autonomy{
			Flag:    "--allow-all-tools",
			Aliases: []string{"--allow-all", "--yolo"},
		},
		RecorderTool: "copilot",
	},
	{
		// GEMINI_CONFIG_DIR is the env var Gemini CLI reads for its config
		// directory (default ~/.gemini); record/gemini.go already resolves a
		// session's transcript store through it via agentHome.
		Exe:          "gemini",
		ConfigDirVar: "GEMINI_CONFIG_DIR",
		Autonomy: &Autonomy{
			Flag:    "--yolo",
			Aliases: []string{"--approval-mode"},
		},
		RecorderTool: "gemini",
	},
	{
		// cursor-agent has no documented config-dir override: its CLI
		// reference (cursor.com/docs/cli/reference/parameters) lists only
		// CURSOR_API_KEY, and its config always lives under ~/.cursor. So
		// ConfigDirVar stays "" — a declared absence, not an omission — and
		// two cursor-agent sessions against different accounts share global
		// config until Cursor documents an override.
		Exe:          "cursor-agent",
		Autonomy:     &Autonomy{Flag: "--force"},
		RecorderTool: "cursor",
	},
	{
		// Aider has --yes-always (skip-permissions) and a recorder built on
		// its default chat log, .aider.chat.history.md, but no config-dir
		// convention: its own docs (aider.chat/docs/config, aider/args.py)
		// resolve .aider.conf.yml from git root/cwd/home with no directory
		// override env var, so ConfigDirVar stays "" — a declared absence.
		Exe:          "aider",
		Autonomy:     &Autonomy{Flag: "--yes-always"},
		RecorderTool: "aider",
	},
}

// Exes returns the ordered base executable names declared in Agents — the
// presentation order for the create-session menu and the order wasa probes
// PATH in.
func Exes() []string {
	exes := make([]string, len(Agents))
	for i, a := range Agents {
		exes[i] = a.Exe
	}
	return exes
}

// ByExe returns the declared agent for exe and whether one is declared.
func ByExe(exe string) (Agent, bool) {
	for _, a := range Agents {
		if a.Exe == exe {
			return a, true
		}
	}
	return Agent{}, false
}

// ByRecorderTool returns the declared agent bound to tool, a recorder's
// Tool() value, and whether one is declared.
func ByRecorderTool(tool string) (Agent, bool) {
	for _, a := range Agents {
		if a.RecorderTool == tool {
			return a, true
		}
	}
	return Agent{}, false
}

// ConfigDirVar returns the config-dir environment variable declared for
// program — matched against an agent's Exe or one of its ConfigDirAliases —
// and whether one is known. A program with no known convention reports
// false.
func ConfigDirVar(program string) (string, bool) {
	for _, a := range Agents {
		if a.ConfigDirVar == "" {
			continue
		}
		if a.Exe == program || slices.Contains(a.ConfigDirAliases, program) {
			return a.ConfigDirVar, true
		}
	}
	return "", false
}
