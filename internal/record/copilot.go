package record

import (
	"encoding/json"
	"path/filepath"
	"time"
)

// copilotRecorder records GitHub Copilot CLI sessions. Unlike every other
// agent, its hooks cannot live in the repository: Copilot does not execute
// .github/hooks/*.json. Measured — a session with that file correctly installed
// ran for twenty minutes, wrote a 655KB transcript and fired no hook, in a
// worktree and again in the long-trusted main repo. The user-level location
// ~/.copilot/hooks/ does fire, which is where sessionstatus has always put its
// own hook.
//
// So the two concerns are split. The user-level file is the delivery mechanism
// and is shared by every repository on the machine; the repository file is the
// per-repo enablement marker, which is what HooksInstalled reports and what
// HandleEvent checks before recording anything. Recording therefore stays
// per-repository — a Copilot session in a repo that never ran `wasa record
// enable` reaches the handler and is dropped.
//
// The user-level hook is left in place by RemoveHooks. It is inert without a
// repository marker, and removing it would break every other repo that still
// has recording enabled.
//
// Its transcript is events.jsonl under ~/.copilot/session-state/<session>/, one
// event per line as {type, timestamp, data}.
type copilotRecorder struct{}

var _ Recorder = copilotRecorder{}
var _ machineWideHook = copilotRecorder{}

func (copilotRecorder) Tool() string { return "copilot" }
func (copilotRecorder) Exe() string  { return "copilot" }

// machineWideHook marks this recorder's hook as shared by every repository, so
// HandleEvent checks the repository marker before recording.
func (copilotRecorder) machineWideHook() {}

func (copilotRecorder) InstallHooks(dir, wasaExe string) error {
	events := []hookEvent{
		{name: "userPromptSubmitted"},
		{name: "postToolUse"},
		{name: "sessionEnd", end: true},
	}
	if err := installFlat(
		copilotHookFile(dir), dir, "copilot", wasaExe, events, copilotEntry,
	); err != nil {
		return err
	}
	return installFlat(
		copilotUserHookFile(), "", "copilot", wasaExe, events, copilotEntry,
	)
}

func (copilotRecorder) RemoveHooks(dir string) error {
	return removeFlat(copilotHookFile(dir))
}

func (copilotRecorder) HooksInstalled(dir string) bool {
	return flatInstalled(copilotHookFile(dir))
}

func (copilotRecorder) LocateTranscript(sessionID, repoDir string) string {
	return existing(copilotTranscriptPath(sessionID, repoDir))
}

func (copilotRecorder) TranscriptTarget(sessionID, repoDir string) string {
	return copilotTranscriptPath(sessionID, repoDir)
}

func (copilotRecorder) ResumeArgs(sessionID string) []string {
	return resumeFlag(sessionID)
}

func (copilotRecorder) Normalize(native []byte) []Message {
	return normalizeJSONL(native, copilotLine)
}

func (copilotRecorder) Intent(native []byte) string {
	return firstUserIntent(normalizeJSONL(native, copilotLine))
}

// copilotTranscriptPath is ~/.copilot/session-state/<session>/events.jsonl,
// computed without checking that the file exists.
func copilotTranscriptPath(sessionID, _ string) string {
	return filepath.Join(
		agentHome("", ".copilot"),
		"session-state", sessionID, "events.jsonl",
	)
}

// copilotLine reads one Copilot events.jsonl line. user.message and
// assistant.message are turns, with text in data.content; the timestamp may be
// epoch-millis or RFC3339. Every other event yields an empty role.
func copilotLine(line []byte) (string, string, time.Time) {
	var l struct {
		Type      string          `json:"type"`
		Timestamp json.RawMessage `json:"timestamp"`
		Data      struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	if json.Unmarshal(line, &l) != nil {
		return "", "", time.Time{}
	}
	ts := parseEpochOrRFC3339(l.Timestamp)
	switch l.Type {
	case "user.message":
		return "user", l.Data.Content, ts
	case "assistant.message":
		return "assistant", l.Data.Content, ts
	}
	return "", "", ts
}
