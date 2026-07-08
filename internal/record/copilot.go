package record

import (
	"encoding/json"
	"path/filepath"
	"time"
)

// copilotRecorder records GitHub Copilot CLI sessions. Its recording hooks live
// in the repository at .github/hooks/wasa.json — the repo-level hook location
// Copilot reads (alongside any other .github/hooks/*.json) — so recording is
// per-repository like every other agent. Copilot only runs repo-level hooks in
// a folder it trusts, so install also registers the repo in Copilot's
// trustedFolders (see ensureCopilotTrusted). Its transcript is events.jsonl
// under ~/.copilot/session-state/<session>/, one event per line as
// {type, timestamp, data}.
type copilotRecorder struct{}

var _ Recorder = copilotRecorder{}

func (copilotRecorder) Tool() string { return "copilot" }
func (copilotRecorder) Exe() string  { return "copilot" }

func (copilotRecorder) InstallHooks(dir, wasaExe string) error {
	if err := installFlat(
		copilotHookFile(dir),
		dir, "copilot", wasaExe,
		[]hookEvent{
			{name: "userPromptSubmitted"},
			{name: "postToolUse"},
			{name: "sessionEnd", end: true},
		},
		copilotEntry,
	); err != nil {
		return err
	}
	return ensureCopilotTrusted(dir)
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
