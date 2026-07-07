package record

import (
	"bufio"
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

// maxTranscriptLine bounds a single transcript line; Claude Code lines carry
// whole tool results, which can be large.
const maxTranscriptLine = 16 << 20

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

// contextBlock builds a case-insensitive, DOTALL pattern matching a balanced
// XML-ish block <name …>…</name> (RE2 has no backreferences, so the tag name
// is fixed per pattern).
func contextBlock(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?is)<` + name + `\b[^>]*>.*?</` + name + `>`)
}

// intentStrippers are the editor/IDE and Claude Code context wrappers that get
// injected around or alongside a typed prompt. They carry machine context, not
// what the human asked, so sanitizeIntent removes them from the extracted
// intent — for live recording and import alike.
var intentStrippers = []*regexp.Regexp{
	contextBlock("system-reminder"),
	contextBlock("context"),
	contextBlock("command-name"),
	contextBlock("command-message"),
	contextBlock("command-args"),
	contextBlock("local-command-stdout"),
	contextBlock("local-command-stderr"),
	// IDE integration blocks: <ide_selection>…</ide_selection>, <ide-opened-
	// file>…, and the like — any ide_/ide- prefixed tag.
	regexp.MustCompile(
		`(?is)<ide[_-][a-z0-9_-]*\b[^>]*>.*?</ide[_-][a-z0-9_-]*>`,
	),
}

// sanitizeIntent strips injected context/IDE wrappers from an extracted intent
// so titles and search show what the human actually typed, then trims the
// remainder. Shared by live recording and import: one extractor, one behavior.
func sanitizeIntent(text string) string {
	for _, re := range intentStrippers {
		text = re.ReplaceAllString(text, "")
	}
	return strings.TrimSpace(text)
}

// isWrapper reports whether a user entry is Claude Code plumbing (slash
// command expansion, command output echo) rather than a typed prompt.
func isWrapper(text string) bool {
	for _, p := range []string{
		"<command-name>", "<local-command-stdout>", "<system-reminder>",
	} {
		if strings.HasPrefix(text, p) {
			return true
		}
	}
	return false
}

// contentText flattens a message content field, which is either a plain
// string or an array of typed blocks, into its text. Non-text blocks (tool
// results, images) contribute nothing.
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type == "text" {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}
