package record

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// historySameBranch caps how many recent same-branch sessions seed the preamble
// before keyword matches are considered; a handful is enough context without
// drowning the task.
const historySameBranch = 3

// HistoryPreamble selects prior checkpoints relevant to a new session on branch
// with the given intent and renders a delimited "recorded history" block to
// prepend to the launch prompt: the intent, outcome and closing turns of each
// selected session, listing session ids so the agent can ask for more via
// `wasa checkpoints show`. Selection is deliberately simple — the newest
// same-branch sessions plus sessions whose recorded intent shares keywords with
// intent — and the whole block is capped at capBytes. It returns "" when the
// repo has no checkpoint ref or nothing relevant is found, so the caller injects
// no noise. The scan is linear over every checkpoint ref, like the search
// command; an index is only worth it once a repo reaches tens of thousands of
// checkpoints.
func HistoryPreamble(repoDir, branch, intent string, capBytes int) string {
	if capBytes <= 0 {
		return ""
	}
	refs, err := forEachRef(repoDir)
	if err != nil || len(refs) == 0 {
		return ""
	}

	kws := keywords(intent)

	type cand struct {
		entry Entry
		score int
		same  bool
	}
	var cands []cand
	seen := map[string]bool{}
	for _, r := range refs {
		if seen[r.session] {
			continue
		}
		seen[r.session] = true
		e := readEntry(repoDir, r)
		recorded, _ := gitIn(repoDir, nil, "show", r.sha+":intent.md")
		cands = append(cands, cand{
			entry: e,
			score: keywordScore(recorded, kws),
			same:  branch != "" && e.Meta.Branch == branch,
		})
	}

	var selected []Entry
	picked := map[string]bool{}
	n := 0
	for _, c := range cands {
		if c.same && n < historySameBranch {
			selected = append(selected, c.entry)
			picked[c.entry.Meta.SessionID] = true
			n++
		}
	}
	kwMatches := make([]cand, 0, len(cands))
	for _, c := range cands {
		if c.score > 0 && !picked[c.entry.Meta.SessionID] {
			kwMatches = append(kwMatches, c)
		}
	}
	sort.SliceStable(kwMatches, func(i, j int) bool {
		return kwMatches[i].score > kwMatches[j].score
	})
	for _, c := range kwMatches {
		selected = append(selected, c.entry)
	}
	if len(selected) == 0 {
		return ""
	}

	header := "Recorded history from previous sessions in this repo (context " +
		"only — the user's request follows below; run " +
		"`wasa checkpoints show <id>` for the full record):\n\n"

	var b strings.Builder
	b.WriteString(header)
	wrote := false
	for _, e := range selected {
		block := historyBlock(repoDir, e)
		if b.Len()+len(block) > capBytes {
			break
		}
		b.WriteString(block)
		wrote = true
	}
	if !wrote {
		return ""
	}
	return strings.TrimRight(b.String(), "\n")
}

// historyBlock renders one selected session: a header naming the session id and
// branch, its intent, its outcome (produced commit count and subjects), and the
// last few conversation turns. Transcript and subjects are read lazily here, so
// only selected sessions pay for them.
func historyBlock(repoDir string, e Entry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "── session %s · branch %s · %s ──\n",
		e.Meta.SessionID, branchOrNone(e.Meta.Branch),
		e.When.Format("2006-01-02"),
	)

	intent, _ := gitIn(repoDir, nil, "show", e.CommitSHA+":intent.md")
	if it := truncate(strings.TrimSpace(intent), 300); it != "" {
		fmt.Fprintf(&b, "intent:  %s\n", it)
	}

	fmt.Fprintf(&b, "outcome: %s\n", outcomeLine(repoDir, e.Meta.Commits))

	transcript, _ := gitIn(
		repoDir,
		nil,
		"show",
		e.CommitSHA+":transcript.jsonl",
	)
	if tail := recentMessages([]byte(transcript), 3); tail != "" {
		b.WriteString("recent:\n")
		for _, ln := range strings.Split(tail, "\n") {
			b.WriteString("  ")
			b.WriteString(ln)
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	return b.String()
}

// outcomeLine summarises what a session produced: the commit count and each
// commit's subject, or a note when it produced none. Subjects are not stored in
// the checkpoint (only SHAs), so they are looked up now; a SHA that no longer
// resolves is shown short rather than dropped.
func outcomeLine(repoDir string, commits []string) string {
	if len(commits) == 0 {
		return "no commits recorded"
	}
	subjects := make([]string, 0, len(commits))
	for _, sha := range commits {
		if s, err := gitIn(
			repoDir, nil, "show", "-s", "--format=%s", sha,
		); err == nil && strings.TrimSpace(s) != "" {
			subjects = append(subjects, strings.TrimSpace(s))
			continue
		}
		subjects = append(subjects, shortSHA(sha))
	}
	return fmt.Sprintf(
		"%d commit(s): %s", len(commits), strings.Join(subjects, "; "),
	)
}

// keywords splits intent into distinct lowercased search terms: word runs of at
// least three characters that are not common filler. It is the input to keyword
// overlap scoring against recorded intents.
func keywords(intent string) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(strings.ToLower(intent), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		if len(f) < 3 || historyStopwords[f] || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// keywordScore counts how many of kws appear in recorded, case-insensitively —
// the same literal-substring notion the search command uses.
func keywordScore(recorded string, kws []string) int {
	r := strings.ToLower(recorded)
	n := 0
	for _, k := range kws {
		if strings.Contains(r, k) {
			n++
		}
	}
	return n
}

// historyStopwords drops a few common words that carry no selection signal;
// three-letter-plus fillers only, since shorter words are excluded by length.
var historyStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true,
	"this": true, "from": true, "into": true, "you": true, "are": true,
}

func branchOrNone(branch string) string {
	if branch == "" {
		return "(none)"
	}
	return branch
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}
