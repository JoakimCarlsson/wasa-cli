package record

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// aiderRecorder records Aider sessions. Unlike every other supported agent,
// Aider has no hook mechanism: its only lifecycle callback,
// --notifications-command, fires once when a response is ready and carries
// no session/event payload, so there is nothing for InstallHooks to write or
// HooksInstalled to detect. What is achievable instead is picked up
// passively at `wasa finish` (see Finish's transcript fallback in handle.go)
// from Aider's own chat log, which it appends to by default at
// <repoDir>/.aider.chat.history.md (AIDER_CHAT_HISTORY_FILE can rename or
// relocate it; that override is not modeled here). The result: an Aider
// session gets a closing checkpoint with the final transcript and full
// commit list, but no per-commit checkpoints and no live intent capture
// during the session — the same shape wasa already gives an unmanaged
// session with "no hook data received".
type aiderRecorder struct{}

var _ Recorder = aiderRecorder{}

func (aiderRecorder) Tool() string { return "aider" }
func (aiderRecorder) Exe() string  { return "aider" }

func (aiderRecorder) InstallHooks(_, _ string) error { return nil }
func (aiderRecorder) RemoveHooks(_ string) error     { return nil }
func (aiderRecorder) HooksInstalled(_ string) bool   { return false }

// LocateTranscript resolves Aider's default chat history file at the
// repository root. sessionID is unused: the file is per-directory, not
// per-session — concurrent Aider sessions in the same worktree share it.
func (aiderRecorder) LocateTranscript(_, repoDir string) string {
	return existing(aiderHistoryPath(repoDir))
}

// TranscriptTarget reports no deterministic per-session path: Aider's chat
// log is shared across every session run in a directory, not keyed by
// session id, so there is nowhere session-specific to restore one to.
func (aiderRecorder) TranscriptTarget(string, string) string { return "" }

// ResumeArgs reports no native resume. Aider's --restore-chat-history flag
// replays the shared chat log wholesale rather than continuing one specific
// session, so it does not fit the per-session resume contract every other
// agent implements here; a resumed Aider session always takes the
// checkpoint preamble instead.
func (aiderRecorder) ResumeArgs(string) []string { return nil }

func (aiderRecorder) Normalize(native []byte) []Message {
	return normalizeAiderHistory(native)
}

func (aiderRecorder) Intent(native []byte) string {
	return firstUserIntent(normalizeAiderHistory(native))
}

// aiderHistoryPath is Aider's default chat history file,
// <repoDir>/.aider.chat.history.md.
func aiderHistoryPath(repoDir string) string {
	return filepath.Join(repoDir, ".aider.chat.history.md")
}

// normalizeAiderHistory reads Aider's markdown chat log into messages.
// Aider appends each user prompt as one or more lines prefixed "#### " (one
// per input line) and each response as unprefixed text, with a
// "# aider chat started at ..." banner marking a new session and "> "
// blockquote lines carrying tool/command output rather than typed or
// generated text. A contiguous run of same-role lines becomes one message;
// the banner and blockquote lines are dropped rather than attributed to
// either role. Aider's log carries no per-line timestamp.
func normalizeAiderHistory(native []byte) []Message {
	var msgs []Message
	var role string
	var buf []string

	flush := func() {
		defer func() { buf = buf[:0] }()
		if role == "" || len(buf) == 0 {
			return
		}
		content := strings.TrimSpace(strings.Join(buf, "\n"))
		if content == "" {
			return
		}
		raw, _ := json.Marshal(content)
		msgs = append(msgs, Message{Role: role, Content: content, Raw: raw})
	}

	for line := range strings.SplitSeq(string(native), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "# aider chat started at"):
			flush()
			role = ""
		case strings.HasPrefix(line, "#### "):
			if role != "user" {
				flush()
				role = "user"
			}
			buf = append(buf, strings.TrimSuffix(
				strings.TrimPrefix(line, "#### "), "  ",
			))
		case strings.HasPrefix(trimmed, ">"):
			flush()
			role = ""
		case trimmed == "":
			if role == "assistant" {
				buf = append(buf, "")
			}
		default:
			if role != "assistant" {
				flush()
				role = "assistant"
			}
			buf = append(buf, line)
		}
	}
	flush()
	return msgs
}
