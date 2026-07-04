package record

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Entry is one checkpoint read back from the ref.
type Entry struct {
	// CommitSHA is the checkpoint commit on the ref.
	CommitSHA string
	// When is the checkpoint's commit time.
	When time.Time
	// Meta is the checkpoint's parsed meta.json; when the blob is missing or
	// malformed only SessionID (from the commit subject) is filled in.
	Meta Meta
}

// List returns the newest checkpoint of every recorded session, newest
// first. A repository without the ref returns an empty list, which is how a
// clone that never fetched the record looks.
func List(repoDir string) ([]Entry, error) {
	lines, err := logLines(repoDir)
	if err != nil || len(lines) == 0 {
		return nil, err
	}
	var (
		entries []Entry
		seen    = map[string]bool{}
	)
	for _, l := range lines {
		if seen[l.session] {
			continue
		}
		seen[l.session] = true
		entries = append(entries, readEntry(repoDir, l))
	}
	return entries, nil
}

// Find returns the newest checkpoint whose session id equals query or
// uniquely starts with it, along with the checkpoint's intent and redacted
// transcript. It errors when nothing matches or the query is ambiguous.
func Find(repoDir, query string) (Entry, string, []byte, error) {
	lines, err := logLines(repoDir)
	if err != nil {
		return Entry{}, "", nil, err
	}
	var (
		match    *logLine
		matchIDs = map[string]bool{}
	)
	for i, l := range lines {
		if l.session != query && !strings.HasPrefix(l.session, query) {
			continue
		}
		if l.session == query {
			matchIDs = map[string]bool{query: true}
			match = &lines[i]
			break
		}
		if !matchIDs[l.session] && match == nil {
			match = &lines[i]
		}
		matchIDs[l.session] = true
	}
	switch {
	case match == nil:
		return Entry{}, "", nil, fmt.Errorf(
			"no recorded session matches %q", query,
		)
	case len(matchIDs) > 1:
		return Entry{}, "", nil, fmt.Errorf(
			"%q is ambiguous: %d recorded sessions match", query,
			len(matchIDs),
		)
	}

	entry := readEntry(repoDir, *match)
	intent, _ := gitIn(
		repoDir, nil, "show", match.sha+":intent.md",
	)
	transcript, _ := gitIn(
		repoDir, nil, "show", match.sha+":transcript.jsonl",
	)
	return entry, intent, []byte(transcript), nil
}

// logLine is one parsed line of git log over the checkpoint ref.
type logLine struct {
	sha     string
	when    time.Time
	session string
}

// logLines lists every checkpoint commit on the ref, newest first. A missing
// ref yields nil, nil: no record is a normal state, not an error.
func logLines(repoDir string) ([]logLine, error) {
	if _, err := gitIn(
		repoDir, nil, "rev-parse", "--verify", "-q", RefName+"^{commit}",
	); err != nil {
		return nil, nil
	}
	out, err := gitIn(
		repoDir, nil, "log", "--format=%H\x1f%ct\x1f%s", RefName, "--",
	)
	if err != nil {
		return nil, err
	}
	var lines []logLine
	for raw := range strings.SplitSeq(out, "\n") {
		parts := strings.SplitN(strings.TrimSpace(raw), "\x1f", 3)
		if len(parts) != 3 {
			continue
		}
		ts, _ := strconv.ParseInt(parts[1], 10, 64)
		lines = append(lines, logLine{
			sha:     parts[0],
			when:    time.Unix(ts, 0),
			session: parts[2],
		})
	}
	return lines, nil
}

// readEntry loads a checkpoint's meta.json, degrading to the subject-derived
// session id when the blob is missing or malformed.
func readEntry(repoDir string, l logLine) Entry {
	e := Entry{CommitSHA: l.sha, When: l.when}
	e.Meta.SessionID = l.session
	if raw, err := gitIn(
		repoDir, nil, "show", l.sha+":meta.json",
	); err == nil {
		var m Meta
		if json.Unmarshal([]byte(raw), &m) == nil && m.SessionID != "" {
			e.Meta = m
		}
	}
	return e
}
