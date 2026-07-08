package record

import (
	"bufio"
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"
)

// claudeRecorder records Claude Code sessions. Its transcript is JSONL under
// ~/.claude/projects/<sanitized-repo>/<session>.jsonl, one message per line
// with the turn kind in the top-level "type" field.
type claudeRecorder struct{}

var _ Recorder = claudeRecorder{}

func (claudeRecorder) Tool() string { return "claude" }
func (claudeRecorder) Exe() string  { return "claude" }

func (claudeRecorder) InstallHooks(dir, wasaExe string) error {
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
}

func (claudeRecorder) RemoveHooks(dir string) error {
	return removeNested(settingsFile(dir, ".claude", "settings.json"), nil)
}

func (claudeRecorder) HooksInstalled(dir string) bool {
	return nestedInstalled(settingsFile(dir, ".claude", "settings.json"))
}

func (claudeRecorder) LocateTranscript(sessionID, repoDir string) string {
	return existing(claudeTranscriptPath(sessionID, repoDir))
}

func (claudeRecorder) TranscriptTarget(sessionID, repoDir string) string {
	return claudeTranscriptPath(sessionID, repoDir)
}

func (claudeRecorder) ResumeArgs(sessionID string) []string {
	return resumeFlag(sessionID)
}

func (claudeRecorder) Normalize(native []byte) []Message {
	return normalizeJSONL(native, claudeLine)
}

func (claudeRecorder) Intent(native []byte) string {
	return FirstUserMessage(native)
}

// claudeTranscriptPath is ~/.claude/projects/<sanitized-repo>/<session>.jsonl,
// computed without checking that the file exists so a resume can restore it.
func claudeTranscriptPath(sessionID, repoDir string) string {
	return filepath.Join(
		agentHome("CLAUDE_CONFIG_DIR", ".claude"),
		"projects", sanitizePath(repoDir), sessionID+".jsonl",
	)
}

// claudeLine reads one Claude Code transcript line: role from "type" (only
// user/assistant are turns), content from message.content, and the top-level
// RFC3339 timestamp. Meta and non-turn lines yield an empty role so they are
// preserved as raw context but never rendered as turns.
func claudeLine(line []byte) (string, string, time.Time) {
	var l struct {
		Type      string `json:"type"`
		IsMeta    bool   `json:"isMeta"`
		Timestamp string `json:"timestamp"`
		Message   struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &l) != nil {
		return "", "", time.Time{}
	}
	ts := parseRFC3339(l.Timestamp)
	if l.IsMeta || (l.Type != "user" && l.Type != "assistant") {
		return "", "", ts
	}
	return l.Type, contentText(l.Message.Content), ts
}

// FirstUserMessage extracts the first real user prompt from a Claude Code
// transcript (JSONL, one message per line): the intent that started the
// session. Meta entries, command wrappers and unparseable lines are skipped.
// It returns "" when the transcript holds no user text.
func FirstUserMessage(transcript []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(transcript))
	sc.Buffer(make([]byte, 0, 64<<10), maxTranscriptLine)
	for sc.Scan() {
		var line struct {
			Type    string `json:"type"`
			IsMeta  bool   `json:"isMeta"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			continue
		}
		if line.Type != "user" || line.IsMeta {
			continue
		}
		text := contentText(line.Message.Content)
		if text == "" || isWrapper(strings.TrimSpace(text)) {
			continue
		}
		text = sanitizeIntent(text)
		if text == "" {
			continue
		}
		return text
	}
	return ""
}
