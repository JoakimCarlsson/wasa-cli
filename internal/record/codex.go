package record

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"
)

// codexRecorder records OpenAI Codex CLI sessions. Its hooks live in a nested
// hooks.json in the worktree (Codex also needs a config.toml feature flag),
// and its transcript is rollout JSONL under
// ~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<session>.jsonl, one event per
// line as {timestamp, type, payload}. Codex has no session-end hook, so an
// unmanaged Codex session closes only through wasa finish.
type codexRecorder struct{}

var _ Recorder = codexRecorder{}

func (codexRecorder) Tool() string { return "codex" }
func (codexRecorder) Exe() string  { return "codex" }

func (codexRecorder) InstallHooks(dir, wasaExe string) error {
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
}

func (codexRecorder) RemoveHooks(dir string) error {
	if err := removeNested(
		settingsFile(dir, ".codex", "hooks.json"), nil,
	); err != nil {
		return err
	}
	removeCodexFeature(dir)
	return nil
}

func (codexRecorder) HooksInstalled(dir string) bool {
	return nestedInstalled(settingsFile(dir, ".codex", "hooks.json"))
}

// LocateTranscript globs the dated session store
// ~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<session>.jsonl.
func (codexRecorder) LocateTranscript(sessionID, _ string) string {
	m, _ := filepath.Glob(filepath.Join(
		agentHome("CODEX_HOME", ".codex"),
		"sessions", "*", "*", "*", "rollout-*-"+sessionID+".jsonl",
	))
	if len(m) == 0 {
		return ""
	}
	return m[len(m)-1]
}

func (codexRecorder) TranscriptTarget(string, string) string { return "" }

func (codexRecorder) ResumeArgs(sessionID string) []string {
	return []string{"resume", sessionID}
}

func (codexRecorder) Normalize(native []byte) []Message {
	return normalizeJSONL(native, codexLine)
}

func (codexRecorder) Intent(native []byte) string {
	return firstUserIntent(normalizeJSONL(native, codexLine))
}

// codexLine reads one Codex rollout line. Only response_item message events
// are turns: role from payload.role, text from the payload's content blocks
// (input_text for the user, output_text for the assistant); system-injected
// user blocks (environment context, AGENTS.md) are dropped so the intent reads
// as what the human typed. Every other line yields an empty role.
func codexLine(line []byte) (string, string, time.Time) {
	var l struct {
		Timestamp string          `json:"timestamp"`
		Type      string          `json:"type"`
		Payload   json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(line, &l) != nil {
		return "", "", time.Time{}
	}
	ts := parseRFC3339(l.Timestamp)
	if l.Type != "response_item" {
		return "", "", ts
	}
	var p struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(l.Payload, &p) != nil || p.Type != "message" {
		return "", "", ts
	}
	if p.Role != "user" && p.Role != "assistant" {
		return "", "", ts
	}
	var b strings.Builder
	for _, blk := range p.Content {
		if blk.Type != "input_text" && blk.Type != "output_text" &&
			blk.Type != "text" {
			continue
		}
		if p.Role == "user" && isCodexSystemContent(blk.Text) {
			continue
		}
		b.WriteString(blk.Text)
	}
	return p.Role, b.String(), ts
}

// isCodexSystemContent reports whether a user content block is context Codex
// injects rather than a typed prompt, mirroring the blocks the reference
// importer drops.
func isCodexSystemContent(text string) bool {
	t := strings.TrimSpace(text)
	for _, p := range []string{
		"<environment_context>", "<permissions", "# AGENTS.md",
		"<user_instructions>",
	} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}
