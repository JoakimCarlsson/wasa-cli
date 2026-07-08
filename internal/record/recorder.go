package record

import (
	"fmt"
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

// Recorder is one agent's recording integration: how to install, remove and
// detect its hook configuration, where to find its transcript, how to resume
// it natively, and how to read its native transcript into the common message
// shape. It is the only agent-aware seam in the recorder — everything else
// operates on the normalized shape or on Meta.Agent, so a new agent is one
// file implementing this interface plus an entry in recorders.
type Recorder interface {
	// Tool is the --tool value and the agent name recorded in meta.json.
	Tool() string
	// Exe is the base executable that selects this agent at launch.
	Exe() string
	// InstallHooks merges the recording hooks into dir's agent configuration.
	InstallHooks(dir, wasaExe string) error
	// RemoveHooks strips the recording hooks from dir's agent configuration.
	RemoveHooks(dir string) error
	// HooksInstalled reports whether dir carries the recording hooks.
	HooksInstalled(dir string) bool
	// LocateTranscript best-effort finds the agent's existing transcript for a
	// session when a hook payload did not name it, or "" when none is present.
	LocateTranscript(sessionID, repoDir string) string
	// TranscriptTarget computes where the agent's transcript for a session
	// would live without checking that it exists, so a resumed session can
	// restore a recorded transcript there before a native resume. "" means the
	// agent has no deterministic single-file transcript, so a session with no
	// live local transcript falls back to the checkpoint preamble.
	TranscriptTarget(sessionID, repoDir string) string
	// ResumeArgs returns the argv appended to the launch program to continue a
	// prior session natively (e.g. {"--resume", id}), or nil for an agent with
	// no CLI resume.
	ResumeArgs(sessionID string) []string
	// Normalize maps the agent's native transcript to the common message shape,
	// one Message per native line, preserving order and the raw bytes.
	Normalize(native []byte) []Message
	// Intent extracts the first real user prompt from the native transcript:
	// the intent that started the session, or "" when there is none.
	Intent(native []byte) string
}

// recorders are the supported recording integrations. Event choices per agent:
// a prompt-bearing event early (intent), a per-tool or per-turn event
// (commit detection) and, where the agent offers one, a session-end event
// (closing checkpoint for unmanaged sessions; Codex has none, so unmanaged
// Codex sessions close only through wasa finish).
var recorders = []Recorder{
	claudeRecorder{},
	geminiRecorder{},
	codexRecorder{},
	copilotRecorder{},
	cursorRecorder{},
}

// recorderFor returns the integration for a --tool value.
func recorderFor(tool string) (Recorder, bool) {
	for _, r := range recorders {
		if r.Tool() == tool {
			return r, true
		}
	}
	return nil, false
}

// AgentForProgram returns the recording tool name for a launch program
// ("/usr/bin/cursor-agent --foo" → "cursor"), or false when the agent has no
// recording integration.
func AgentForProgram(program string) (string, bool) {
	exe := baseExe(program)
	for _, r := range recorders {
		if r.Exe() == exe {
			return r.Tool(), true
		}
	}
	return "", false
}

// resumeFlag is the common native-resume argv "--resume <id>", shared by the
// agents whose CLI resumes a session that way (claude, gemini, copilot).
func resumeFlag(id string) []string { return []string{"--resume", id} }

// ResumeArgs returns the argv to append to a launch program so its agent
// continues the session agentSessionID natively (e.g. {"--resume", id}), and
// whether that agent supports native resume at all. An empty agentSessionID or
// an agent with no CLI resume reports false, and the caller falls back to the
// checkpoint preamble.
func ResumeArgs(program, agentSessionID string) ([]string, bool) {
	if agentSessionID == "" {
		return nil, false
	}
	tool, ok := AgentForProgram(program)
	if !ok {
		return nil, false
	}
	r, _ := recorderFor(tool)
	args := r.ResumeArgs(agentSessionID)
	if args == nil {
		return nil, false
	}
	return args, true
}

// LocalTranscript returns the path to the live local transcript program's agent
// keeps for agentSessionID under worktreePath, or "" when none is present. A
// present transcript means a native resume can continue without restoring
// anything.
func LocalTranscript(program, agentSessionID, worktreePath string) string {
	tool, ok := AgentForProgram(program)
	if !ok || agentSessionID == "" {
		return ""
	}
	r, _ := recorderFor(tool)
	return r.LocateTranscript(agentSessionID, worktreePath)
}

// RestoreTranscript writes the recorded transcript to where program's agent
// expects it for agentSessionID under worktreePath, so a native resume finds it
// when the live local transcript is gone (e.g. resuming on another machine).
// data is the stored (normalized) transcript; it is converted back to the
// agent's native bytes before writing. It errors when the agent has no
// deterministic transcript path — the caller then falls back to the checkpoint
// preamble rather than a native resume.
func RestoreTranscript(
	program, agentSessionID, worktreePath string, data []byte,
) error {
	tool, ok := AgentForProgram(program)
	if !ok {
		return fmt.Errorf("unknown agent for program %q", program)
	}
	r, _ := recorderFor(tool)
	if agentSessionID == "" {
		return fmt.Errorf("agent %q has no restorable transcript path", tool)
	}
	path := r.TranscriptTarget(agentSessionID, worktreePath)
	if path == "" {
		return fmt.Errorf("agent %q has no restorable transcript path", tool)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, denormalize(data), 0o644)
}

// InstallHooks installs tool's recording hooks into dir, a repository root
// or worktree.
func InstallHooks(dir, tool, wasaExe string) error {
	r, ok := recorderFor(tool)
	if !ok {
		return nil
	}
	return r.InstallHooks(dir, wasaExe)
}

// RemoveHooks strips every agent's recording hooks from dir.
func RemoveHooks(dir string) error {
	for _, r := range recorders {
		if err := r.RemoveHooks(dir); err != nil {
			return err
		}
	}
	return nil
}

// InstalledAgents lists the agents whose recording hooks are present in dir.
func InstalledAgents(dir string) []string {
	var tools []string
	for _, r := range recorders {
		if r.HooksInstalled(dir) {
			tools = append(tools, r.Tool())
		}
	}
	return tools
}

// DetectedAgents lists the supported agents found on PATH, which is what
// repo-level enable installs for.
func DetectedAgents() []string {
	var tools []string
	for _, r := range recorders {
		if _, err := exec.LookPath(r.Exe()); err == nil {
			tools = append(tools, r.Tool())
		}
	}
	return tools
}

// fallbackTranscript best-effort locates a transcript for sessions whose
// hook payloads never carried a path (Cursor and Copilot omit it on some
// events; a Codex payload may null it).
func fallbackTranscript(tool, sessionID, repoDir string) string {
	r, ok := recorderFor(tool)
	if !ok || sessionID == "" {
		return ""
	}
	return r.LocateTranscript(sessionID, repoDir)
}

// intentFrom extracts the session intent from a native transcript using the
// agent's own parser, or "" when the agent is unknown or the transcript holds
// no user text.
func intentFrom(tool string, native []byte) string {
	r, ok := recorderFor(tool)
	if !ok {
		return ""
	}
	return r.Intent(native)
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
