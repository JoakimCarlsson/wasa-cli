package record

import (
	"encoding/json"
	"path/filepath"
	"time"
)

// cursorRecorder records Cursor CLI sessions. Its hooks live in a wasa-owned
// flat hooks.json in the worktree, and its transcript is the same Anthropic
// block-shaped JSONL as Claude Code except the turn kind is in "role", not
// "type"; it carries no per-line timestamp. Cursor has no CLI resume, so a
// resumed Cursor session always takes the checkpoint preamble.
type cursorRecorder struct{}

var _ Recorder = cursorRecorder{}

func (cursorRecorder) Tool() string { return "cursor" }
func (cursorRecorder) Exe() string  { return "cursor-agent" }

func (cursorRecorder) InstallHooks(dir, wasaExe string) error {
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
}

func (cursorRecorder) RemoveHooks(dir string) error {
	return removeFlat(settingsFile(dir, ".cursor", "hooks.json"))
}

func (cursorRecorder) HooksInstalled(dir string) bool {
	return flatInstalled(settingsFile(dir, ".cursor", "hooks.json"))
}

// LocateTranscript resolves the CLI (flat) and IDE (nested) layouts Cursor
// uses under ~/.cursor/projects/<sanitized-repo>/agent-transcripts/.
func (cursorRecorder) LocateTranscript(sessionID, repoDir string) string {
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

func (cursorRecorder) TranscriptTarget(string, string) string { return "" }

func (cursorRecorder) ResumeArgs(string) []string { return nil }

func (cursorRecorder) Normalize(native []byte) []Message {
	return normalizeJSONL(native, cursorLine)
}

func (cursorRecorder) Intent(native []byte) string {
	return firstUserIntent(normalizeJSONL(native, cursorLine))
}

// cursorLine reads one Cursor transcript line: role from "role" (falling back
// to "type"), content from message.content (falling back to a top-level
// content field). Cursor carries no per-line timestamp.
func cursorLine(line []byte) (string, string, time.Time) {
	var l struct {
		Role    string          `json:"role"`
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &l) != nil {
		return "", "", time.Time{}
	}
	role := l.Role
	if role == "" {
		role = l.Type
	}
	if role != "user" && role != "assistant" {
		return "", "", time.Time{}
	}
	content := l.Message.Content
	if len(content) == 0 {
		content = l.Content
	}
	return role, contentText(content), time.Time{}
}
