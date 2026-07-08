package record

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"
)

// geminiRecorder records Gemini CLI sessions. Its hooks live in a nested
// settings.json in the worktree (Gemini also needs hooksConfig.enabled). Its
// transcript, unlike every other supported agent, is a single JSON object
// {"sessionId","messages":[...]} under
// ~/.gemini/tmp/<sha256(repo)>/chats/session-*.json, so it has no restorable
// single-line transcript path and a resumed Gemini session with no live local
// transcript falls back to the checkpoint preamble.
type geminiRecorder struct{}

var _ Recorder = geminiRecorder{}

func (geminiRecorder) Tool() string { return "gemini" }
func (geminiRecorder) Exe() string  { return "gemini" }

func (geminiRecorder) InstallHooks(dir, wasaExe string) error {
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
}

func (geminiRecorder) RemoveHooks(dir string) error {
	return removeNested(
		settingsFile(dir, ".gemini", "settings.json"),
		[]string{"hooksConfig"},
	)
}

func (geminiRecorder) HooksInstalled(dir string) bool {
	return nestedInstalled(settingsFile(dir, ".gemini", "settings.json"))
}

// LocateTranscript resolves the per-repo chat store Gemini keeps at
// ~/.gemini/tmp/<sha256(abs repo)>/chats/session-*.json and returns the newest
// one. Best-effort: session files are not named by the agent session id, so a
// repo with concurrent Gemini sessions may pick the wrong file, degrading to a
// finish-time gap rather than an error.
func (geminiRecorder) LocateTranscript(_, repoDir string) string {
	abs, err := filepath.Abs(repoDir)
	if err != nil {
		abs = repoDir
	}
	sum := sha256.Sum256([]byte(abs))
	dir := filepath.Join(
		agentHome("GEMINI_CONFIG_DIR", ".gemini"),
		"tmp", hex.EncodeToString(sum[:]), "chats",
	)
	m, _ := filepath.Glob(filepath.Join(dir, "session-*.json"))
	if len(m) == 0 {
		return ""
	}
	return m[len(m)-1]
}

func (geminiRecorder) TranscriptTarget(string, string) string { return "" }

func (geminiRecorder) ResumeArgs(sessionID string) []string {
	return resumeFlag(sessionID)
}

// Normalize reads Gemini's single-object transcript into one Message per chat
// message, preserving each message's raw JSON. Info/system messages yield an
// empty role so they are kept as raw context but not shown as turns.
func (geminiRecorder) Normalize(native []byte) []Message {
	var doc struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if json.Unmarshal(native, &doc) != nil {
		return nil
	}
	var msgs []Message
	for _, raw := range doc.Messages {
		role, content, ts := geminiMessage(raw)
		msgs = append(msgs, Message{
			Role: role, Content: content, Timestamp: ts, Raw: rawJSON(raw),
		})
	}
	return msgs
}

func (geminiRecorder) Intent(native []byte) string {
	return firstUserIntent(geminiRecorder{}.Normalize(native))
}

// geminiMessage reads one Gemini chat message: type "user" is a user turn,
// "gemini" an assistant turn, everything else (info) is dropped to an empty
// role; content is a plain string or an array of {text} parts.
func geminiMessage(raw json.RawMessage) (string, string, time.Time) {
	var m struct {
		Type      string          `json:"type"`
		Timestamp string          `json:"timestamp"`
		Content   json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return "", "", time.Time{}
	}
	ts := parseRFC3339(m.Timestamp)
	var role string
	switch m.Type {
	case "user":
		role = "user"
	case "gemini":
		role = "assistant"
	default:
		return "", "", ts
	}
	return role, geminiContent(m.Content), ts
}

// geminiContent flattens a Gemini content field: a plain string, or an array
// of parts each carrying text.
func geminiContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String()
}
