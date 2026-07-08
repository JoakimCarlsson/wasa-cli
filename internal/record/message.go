package record

import (
	"bufio"
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// maxTranscriptLine bounds a single transcript line; agent transcripts carry
// whole tool results on one line, which can be large.
const maxTranscriptLine = 16 << 20

// Message is one turn of a session in the common shape every agent's native
// transcript is normalized into, so read-back commands and future consumers
// never branch per agent. Role and Content are the minimum; Timestamp is set
// when the native format carries one; Raw preserves the original native line
// so the transcript can be rendered richly and, for line-based formats,
// reconstructed losslessly for a native resume.
type Message struct {
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	Timestamp time.Time       `json:"timestamp,omitzero"`
	Raw       json.RawMessage `json:"raw,omitempty"`
}

// lineParser reads one native transcript line into the fields of a Message.
// A line it does not recognize (a header, a tool result, plumbing) yields an
// empty role, so it is preserved as raw context but never shown as a turn.
type lineParser func(line []byte) (role, content string, ts time.Time)

// normalize converts an agent's native transcript to the stored normalized
// JSONL. An unknown agent or a transcript the agent's parser makes nothing of
// is returned unchanged, so a checkpoint still carries what was captured and
// the missing-normalization is a display gap, never a lost transcript.
func normalize(tool string, native []byte) []byte {
	if len(native) == 0 {
		return nil
	}
	r, ok := recorderFor(tool)
	if !ok {
		return native
	}
	msgs := r.Normalize(native)
	if len(msgs) == 0 {
		return native
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	for _, m := range msgs {
		if err := enc.Encode(m); err != nil {
			continue
		}
	}
	return b.Bytes()
}

// normalizeJSONL builds the normalized messages for a line-based (JSONL)
// transcript: one Message per non-blank line, in order, each carrying its raw
// bytes, with role/content/timestamp filled by parse. Preserving every line
// (even ones parse does not recognize) keeps the transcript reconstructible.
func normalizeJSONL(native []byte, parse lineParser) []Message {
	var msgs []Message
	sc := bufio.NewScanner(bytes.NewReader(native))
	sc.Buffer(make([]byte, 0, 64<<10), maxTranscriptLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var role, content string
		var ts time.Time
		if parse != nil {
			role, content, ts = parse(line)
		}
		msgs = append(msgs, Message{
			Role: role, Content: content, Timestamp: ts, Raw: rawJSON(line),
		})
	}
	return msgs
}

// rawJSON keeps a native line as a valid JSON value for the Message.Raw field:
// the line itself when it is valid JSON, else the line as a JSON string so a
// truncated or non-JSON line still round-trips through the store.
func rawJSON(line []byte) json.RawMessage {
	if json.Valid(line) {
		return json.RawMessage(append([]byte(nil), line...))
	}
	b, _ := json.Marshal(string(line))
	return b
}

// denormalize reconstructs the native transcript bytes from stored normalized
// JSONL by concatenating each message's raw line, so a native resume finds the
// agent's own format. Data that is not in the normalized shape (a checkpoint
// written before normalization, stored raw) is returned unchanged.
func denormalize(data []byte) []byte {
	if !looksNormalized(data) {
		return data
	}
	var b bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), maxTranscriptLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var m Message
		if json.Unmarshal(line, &m) != nil || len(m.Raw) == 0 {
			continue
		}
		raw := bytes.TrimSpace(m.Raw)
		if len(raw) > 0 && raw[0] == '"' {
			var s string
			if json.Unmarshal(raw, &s) == nil {
				b.WriteString(s)
				b.WriteByte('\n')
				continue
			}
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// looksNormalized reports whether data is stored normalized JSONL rather than
// a raw native transcript, by the presence of the raw field on the first line.
// No native transcript carries a top-level "raw", so this never misfires.
func looksNormalized(data []byte) bool {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), maxTranscriptLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var m map[string]json.RawMessage
		if json.Unmarshal(line, &m) != nil {
			return false
		}
		_, ok := m["raw"]
		return ok
	}
	return false
}

// firstUserIntent returns the first real user prompt from normalized messages:
// the intent that started the session. Wrapper/plumbing turns and injected
// context are skipped, matching the Claude-native extractor's behavior so every
// agent's intent reads as what the human typed.
func firstUserIntent(msgs []Message) string {
	for _, m := range msgs {
		if m.Role != "user" {
			continue
		}
		text := strings.TrimSpace(m.Content)
		if text == "" || isWrapper(text) {
			continue
		}
		if text = sanitizeIntent(text); text != "" {
			return text
		}
	}
	return ""
}

// recentMessages returns the last n user/assistant turns of a stored
// transcript, each truncated, one per line, for the resume preamble. It reads
// the normalized shape; a transcript stored raw before normalization (an older
// checkpoint) falls back to the Claude-native reader so old sessions still
// resume.
func recentMessages(transcript []byte, n int) string {
	if !looksNormalized(transcript) {
		return recentMessagesNative(transcript, n)
	}
	var msgs []string
	sc := bufio.NewScanner(bytes.NewReader(transcript))
	sc.Buffer(make([]byte, 0, 64<<10), maxTranscriptLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var m Message
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		text := strings.TrimSpace(m.Content)
		if text == "" || isWrapper(text) {
			continue
		}
		if len(text) > 500 {
			text = text[:500] + "…"
		}
		msgs = append(msgs, m.Role+": "+text)
	}
	if len(msgs) > n {
		msgs = msgs[len(msgs)-n:]
	}
	return strings.Join(msgs, "\n")
}

// recentMessagesNative reads the last n user/assistant turns from a raw Claude
// Code transcript, for checkpoints recorded before transcripts were normalized.
func recentMessagesNative(transcript []byte, n int) string {
	sc := bufio.NewScanner(bytes.NewReader(transcript))
	sc.Buffer(make([]byte, 0, 64<<10), maxTranscriptLine)
	var msgs []string
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
		if line.IsMeta || (line.Type != "user" && line.Type != "assistant") {
			continue
		}
		text := strings.TrimSpace(contentText(line.Message.Content))
		if text == "" || isWrapper(text) {
			continue
		}
		if len(text) > 500 {
			text = text[:500] + "…"
		}
		msgs = append(msgs, line.Type+": "+text)
	}
	if len(msgs) > n {
		msgs = msgs[len(msgs)-n:]
	}
	return strings.Join(msgs, "\n")
}

// parseRFC3339 parses an RFC3339 (optionally nano) timestamp, returning the
// zero time when the string is empty or unparseable — a missing timestamp is a
// display detail, never a reason to drop a turn.
func parseRFC3339(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

// parseEpochOrRFC3339 parses a timestamp that may be epoch milliseconds (a JSON
// number) or an RFC3339 string, returning the zero time when neither parses.
func parseEpochOrRFC3339(raw json.RawMessage) time.Time {
	raw = json.RawMessage(bytes.TrimSpace(raw))
	if len(raw) == 0 {
		return time.Time{}
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return parseRFC3339(s)
		}
		return time.Time{}
	}
	var ms int64
	if json.Unmarshal(raw, &ms) == nil && ms > 0 {
		return time.UnixMilli(ms).UTC()
	}
	return time.Time{}
}

// RenderTranscript formats a stored transcript for display: one block per
// user/assistant turn, header then indented content, identical regardless of
// which agent produced it. A transcript stored raw before normalization (an
// older checkpoint) is returned as-is, so read-back never breaks.
func RenderTranscript(stored []byte) string {
	if !looksNormalized(stored) {
		return string(stored)
	}
	var b strings.Builder
	sc := bufio.NewScanner(bytes.NewReader(stored))
	sc.Buffer(make([]byte, 0, 64<<10), maxTranscriptLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var m Message
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		content := strings.TrimRight(m.Content, "\n")
		if m.Role == "" || strings.TrimSpace(content) == "" {
			continue
		}
		b.WriteString(m.Role)
		if !m.Timestamp.IsZero() {
			b.WriteString("  ")
			b.WriteString(m.Timestamp.Local().Format("2006-01-02 15:04:05"))
		}
		b.WriteByte('\n')
		for l := range strings.SplitSeq(content, "\n") {
			b.WriteString("  ")
			b.WriteString(l)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	return b.String()
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

// isWrapper reports whether a user entry is agent plumbing (slash command
// expansion, command output echo) rather than a typed prompt.
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

// contentText flattens a message content field, which is either a plain string
// or an array of typed blocks, into its text. Non-text blocks (tool results,
// images) contribute nothing. Shared by the agents whose native content field
// uses the Anthropic block shape (Claude Code, Cursor).
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
