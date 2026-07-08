package record

import (
	"regexp"
	"strings"
	"time"
)

// SearchOpts controls a checkpoint search: the query and how to match and
// filter it. The zero value with a Query is a case-insensitive substring
// search over intents and transcripts, unlimited.
type SearchOpts struct {
	// Query is the text (or regular expression when Regex) to look for.
	Query string
	// Regex matches Query as a regular expression instead of a
	// case-insensitive substring.
	Regex bool
	// IntentOnly searches intent.md only and skips transcripts.
	IntentOnly bool
	// Branch, when set, limits the search to sessions on this branch.
	Branch string
	// Since, when non-zero, skips checkpoints recorded before this instant.
	Since time.Time
	// Limit caps how many matching sessions are returned; <= 0 means 20.
	Limit int
}

// SearchHit is one matching session: its newest matching checkpoint, which
// file matched, and the first matching line with the match span.
type SearchHit struct {
	Entry
	// File is "intent" or "transcript": which blob the match came from.
	File string
	// LineText is the first line of File that matched.
	LineText string
	// Start and End are the byte span of the match within LineText.
	Start, End int
}

// Search scans the checkpoint ref store and returns the newest matching
// checkpoint of every session whose intent or transcript matches opts.Query,
// newest first, at most opts.Limit sessions. It returns ErrNoRecord when the
// repo has no checkpoint ref. The scan is linear, up to three blob reads per
// ref; an index is only worth it if repos reach tens of thousands of
// checkpoints.
func Search(repoDir string, opts SearchOpts) ([]SearchHit, error) {
	re, err := compileMatcher(opts.Query, opts.Regex)
	if err != nil {
		return nil, err
	}
	refs, err := forEachRef(repoDir)
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return nil, ErrNoRecord
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}

	var hits []SearchHit
	seen := map[string]bool{}
	for _, r := range refs {
		if len(hits) >= limit {
			break
		}
		if seen[r.session] {
			continue
		}
		if !opts.Since.IsZero() && r.when.Before(opts.Since) {
			continue
		}
		e := readEntry(repoDir, r)
		if opts.Branch != "" && e.Meta.Branch != opts.Branch {
			continue
		}
		hit, ok := matchCheckpoint(repoDir, r, re, opts.IntentOnly)
		if !ok {
			continue
		}
		hit.Entry = e
		hits = append(hits, hit)
		seen[r.session] = true
	}
	return hits, nil
}

// matchCheckpoint returns the first matching line of the checkpoint's intent,
// then (unless intentOnly) its transcript. Blobs are the stored, redacted
// copies, so a match never surfaces a secret the record already scrubbed.
func matchCheckpoint(
	repoDir string, r refInfo, re *regexp.Regexp, intentOnly bool,
) (SearchHit, bool) {
	intent, _ := gitIn(repoDir, nil, "show", r.sha+":intent.md")
	if line, s, e, ok := firstMatch(re, intent); ok {
		return SearchHit{File: "intent", LineText: line, Start: s, End: e}, true
	}
	if intentOnly {
		return SearchHit{}, false
	}
	transcript, _ := gitIn(repoDir, nil, "show", r.sha+":transcript.jsonl")
	if line, s, e, ok := firstMatch(re, transcript); ok {
		return SearchHit{
			File: "transcript", LineText: line, Start: s, End: e,
		}, true
	}
	return SearchHit{}, false
}

// Snippet trims a matching line and, when it is longer than width, returns a
// window centred on the match with "…" markers, adjusting the match span to the
// returned string. It is pure string arithmetic over byte offsets, so callers
// can style the [hs:he] span however their frontend renders a highlight.
func Snippet(line string, start, end, width int) (out string, hs, he int) {
	trimmed := strings.TrimLeft(line, " \t")
	off := len(line) - len(trimmed)
	line, start, end = trimmed, start-off, end-off
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if len(line) <= width {
		return line, start, end
	}

	pad := (width - (end - start)) / 2
	if pad < 0 {
		pad = 0
	}
	ws := start - pad
	if ws < 0 {
		ws = 0
	}
	we := ws + width
	if we > len(line) {
		we, ws = len(line), len(line)-width
		if ws < 0 {
			ws = 0
		}
	}

	prefix, suffix := "", ""
	if ws > 0 {
		prefix = "…"
	}
	if we < len(line) {
		suffix = "…"
	}
	out = prefix + line[ws:we] + suffix
	hs, he = start-ws+len(prefix), end-ws+len(prefix)
	if he > len(out) {
		he = len(out)
	}
	return out, hs, he
}

// compileMatcher builds the search regexp: the query verbatim when isRegex,
// otherwise a case-insensitive match of the literal query.
func compileMatcher(query string, isRegex bool) (*regexp.Regexp, error) {
	if isRegex {
		return regexp.Compile(query)
	}
	return regexp.Compile("(?i)" + regexp.QuoteMeta(query))
}

// firstMatch returns the first line of content that re matches and the byte
// span of the match within it.
func firstMatch(
	re *regexp.Regexp, content string,
) (line string, start, end int, ok bool) {
	for ln := range strings.SplitSeq(content, "\n") {
		if loc := re.FindStringIndex(ln); loc != nil {
			return ln, loc[0], loc[1], true
		}
	}
	return "", 0, 0, false
}
