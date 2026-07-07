package record

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// preambleTailBudget caps the transcript-tail portion of a resume preamble so a
// resumed agent's first prompt stays reasonable; the intent and commit list are
// small and always kept in full.
const preambleTailBudget = 4000

// BuildPreamble composes the context a resumed session's agent is seeded with
// when native resume is unavailable (the agent has no CLI resume, or its
// transcript could not be restored). It is a plain prompt the agent reads as its
// first message: the original intent, the commits the session already produced,
// and the tail of the recorded conversation, so the agent continues the earlier
// work rather than starting cold. The transcript tail is best-effort — it parses
// the Claude Code transcript shape and yields nothing for agents whose recorded
// transcript is in another format, in which case the intent and commits still
// carry the context.
func BuildPreamble(intent string, m Meta, transcript []byte) string {
	var b strings.Builder
	b.WriteString(
		"You are resuming an earlier session. Continue from where it left " +
			"off — do not restart or re-ask what was already decided.\n\n",
	)

	if intent = strings.TrimSpace(intent); intent != "" {
		b.WriteString("Original request:\n")
		b.WriteString(intent)
		b.WriteString("\n\n")
	}

	if len(m.Commits) > 0 {
		fmt.Fprintf(&b, "Commits already produced (%d):\n", len(m.Commits))
		for _, c := range m.Commits {
			b.WriteString("  - ")
			b.WriteString(shortSHA(c))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if tail := recentMessages(transcript, 6); tail != "" {
		if len(tail) > preambleTailBudget {
			tail = tail[len(tail)-preambleTailBudget:]
		}
		b.WriteString("Recent conversation:\n")
		b.WriteString(tail)
		b.WriteString("\n")
	}

	return strings.TrimSpace(b.String())
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// recentMessages returns the last n user/assistant text turns of a Claude Code
// transcript, each truncated, one per line. It reuses the same content
// flattening as FirstUserMessage and skips wrapper/meta lines; a transcript in
// another agent's format yields "".
func recentMessages(transcript []byte, n int) string {
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
