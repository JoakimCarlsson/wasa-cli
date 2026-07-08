package record

import (
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
