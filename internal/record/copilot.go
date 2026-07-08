package record

import (
	"encoding/json"
	"path/filepath"
	"time"
)

// copilotRecorder records GitHub Copilot CLI sessions. Copilot discovers hooks
// only in its per-user directory, never in a repository, so the recorder is
// installed once per machine at ~/.copilot/hooks/wasa-record.json (see
// installCopilot) and records Copilot sessions run in any repository. Its
// transcript is events.jsonl under ~/.copilot/session-state/<session>/, one
// event per line as {type, timestamp, data}.
type copilotRecorder struct{}

var _ Recorder = copilotRecorder{}

func (copilotRecorder) Tool() string { return "copilot" }
func (copilotRecorder) Exe() string  { return "copilot" }

func (copilotRecorder) InstallHooks(_, wasaExe string) error {
	return installCopilot(wasaExe, []hookEvent{
		{name: "userPromptSubmitted"},
		{name: "postToolUse"},
		{name: "sessionEnd", end: true},
	})
}

func (copilotRecorder) RemoveHooks(string) error { return removeCopilot() }

func (copilotRecorder) HooksInstalled(string) bool { return copilotInstalled() }

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
