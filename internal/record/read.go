package record

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ErrNoRecord marks a repository that has no checkpoint ref at all: recording
// has never run there (or a clone never fetched the record).
var ErrNoRecord = errors.New("no checkpoint record")

// Entry is one checkpoint read back from the ref store.
type Entry struct {
	// Ref is the full checkpoint ref, refs/wasa/checkpoints/<shard>/<ulid>.
	Ref string
	// ID is the checkpoint ULID (the ref's leaf name).
	ID string
	// CommitSHA is the commit the ref points at.
	CommitSHA string
	// When is the checkpoint's commit time.
	When time.Time
	// Meta is the checkpoint's parsed meta.json; when the blob is missing or
	// malformed only SessionID (from the commit subject) is filled in.
	Meta Meta
}

// List returns the newest checkpoint of every recorded session, newest
// first. A repository without any checkpoint ref returns an empty list,
// which is how a clone that never fetched the record looks.
func List(repoDir string) ([]Entry, error) {
	refs, err := forEachRef(repoDir)
	if err != nil || len(refs) == 0 {
		return nil, err
	}
	var (
		entries []Entry
		seen    = map[string]bool{}
	)
	for _, r := range refs {
		if seen[r.session] {
			continue
		}
		seen[r.session] = true
		entries = append(entries, readEntry(repoDir, r))
	}
	return entries, nil
}

// Find resolves query to one checkpoint and returns it with its intent and
// redacted transcript. A query is matched first as a session id (exact, else
// a unique prefix) resolving to that session's newest checkpoint, then as a
// checkpoint ULID (exact, else a unique prefix). It errors when nothing
// matches or the query is ambiguous.
func Find(repoDir, query string) (Entry, string, []byte, error) {
	refs, err := forEachRef(repoDir)
	if err != nil {
		return Entry{}, "", nil, err
	}

	match, err := findBySession(refs, query)
	if err != nil {
		return Entry{}, "", nil, err
	}
	if match == nil {
		match, err = findByID(refs, query)
		if err != nil {
			return Entry{}, "", nil, err
		}
	}
	if match == nil {
		return Entry{}, "", nil, fmt.Errorf(
			"no recorded session matches %q", query,
		)
	}

	entry := readEntry(repoDir, *match)
	intent, _ := gitIn(repoDir, nil, "show", match.sha+":intent.md")
	transcript, _ := gitIn(repoDir, nil, "show", match.sha+":transcript.jsonl")
	return entry, intent, []byte(transcript), nil
}

// findBySession returns the newest ref whose session id equals query or
// uniquely starts with it, nil when none match, or an error when a prefix is
// ambiguous across sessions. refs are newest first, so the first match for a
// session is that session's newest checkpoint.
func findBySession(refs []refInfo, query string) (*refInfo, error) {
	var (
		match    *refInfo
		matchIDs = map[string]bool{}
	)
	for i, r := range refs {
		if r.session == query {
			return &refs[i], nil
		}
		if !strings.HasPrefix(r.session, query) {
			continue
		}
		if match == nil {
			match = &refs[i]
		}
		matchIDs[r.session] = true
	}
	if len(matchIDs) > 1 {
		return nil, fmt.Errorf(
			"%q is ambiguous: %d recorded sessions match", query,
			len(matchIDs),
		)
	}
	return match, nil
}

// findByID returns the ref whose ULID equals query or uniquely starts with
// it, nil when none match, or an error when a prefix is ambiguous.
func findByID(refs []refInfo, query string) (*refInfo, error) {
	var (
		match   *refInfo
		matched int
	)
	for i, r := range refs {
		if r.id == query {
			return &refs[i], nil
		}
		if !strings.HasPrefix(r.id, query) {
			continue
		}
		if match == nil {
			match = &refs[i]
		}
		matched++
	}
	if matched > 1 {
		return nil, fmt.Errorf(
			"%q is ambiguous: %d checkpoints match", query, matched,
		)
	}
	return match, nil
}

// Match is a checkpoint that produced a commit, with its intent and redacted
// transcript, as returned by Explain.
type Match struct {
	Entry
	Intent     string
	Transcript []byte
}

// Explain resolves commitish to a commit and returns the checkpoint(s) whose
// meta.json lists it among produced commits, newest first. When all is false it
// returns at most the newest match. searched is how many checkpoints were
// scanned. It returns ErrNoRecord when the repo has no checkpoint ref, and a
// resolution error when commitish names no commit. The scan is linear, one file
// read per ref; an index is only worth it if repos reach tens of thousands of
// checkpoints.
func Explain(repoDir, commitish string, all bool) ([]Match, int, error) {
	sha, err := gitIn(
		repoDir, nil, "rev-parse", "--verify", "-q", commitish+"^{commit}",
	)
	if err != nil {
		return nil, 0, fmt.Errorf("cannot resolve %q to a commit", commitish)
	}
	refs, err := forEachRef(repoDir)
	if err != nil {
		return nil, 0, err
	}
	if len(refs) == 0 {
		return nil, 0, ErrNoRecord
	}

	var matches []Match
	searched := 0
	for _, r := range refs {
		searched++
		e := readEntry(repoDir, r)
		if !slices.Contains(e.Meta.Commits, sha) {
			continue
		}
		intent, _ := gitIn(repoDir, nil, "show", r.sha+":intent.md")
		transcript, _ := gitIn(repoDir, nil, "show", r.sha+":transcript.jsonl")
		matches = append(matches, Match{
			Entry: e, Intent: intent, Transcript: []byte(transcript),
		})
		if !all {
			break
		}
	}
	return matches, searched, nil
}

// Prune deletes every checkpoint whose commit time is before the given
// instant, locally only, and returns how many refs it removed. Pruning a
// remote is the user's push away. User branches and other refs are untouched.
func Prune(repoDir string, before time.Time) (int, error) {
	refs, err := forEachRef(repoDir)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, r := range refs {
		if !r.when.Before(before) {
			continue
		}
		if _, err := gitIn(repoDir, nil, "update-ref", "-d", r.ref); err != nil {
			return deleted, fmt.Errorf("prune %s: %w", r.ref, err)
		}
		deleted++
	}
	return deleted, nil
}

// refInfo is one checkpoint ref as reported by for-each-ref.
type refInfo struct {
	ref     string
	id      string
	sha     string
	when    time.Time
	session string
}

// forEachRef lists every checkpoint ref, newest first. Ordering is by the
// ULID (the ref's leaf), which is timestamp-prefixed and so sorts
// chronologically; the full refname cannot be used because the shard segment
// — the ULID's last two characters — precedes the ULID in the path and would
// scramble the order. A repository with no checkpoint ref yields nil, nil: no
// record is a normal state, not an error.
func forEachRef(repoDir string) ([]refInfo, error) {
	out, err := gitIn(
		repoDir, nil, "for-each-ref",
		"--format=%(refname)\x1f%(objectname)\x1f%(committerdate:unix)"+
			"\x1f%(contents:subject)",
		RefPrefix+"/",
	)
	if err != nil || out == "" {
		return nil, nil
	}
	var refs []refInfo
	for raw := range strings.SplitSeq(out, "\n") {
		parts := strings.SplitN(strings.TrimSpace(raw), "\x1f", 4)
		if len(parts) != 4 {
			continue
		}
		ts, _ := strconv.ParseInt(parts[2], 10, 64)
		ref := parts[0]
		refs = append(refs, refInfo{
			ref:     ref,
			id:      ref[strings.LastIndexByte(ref, '/')+1:],
			sha:     parts[1],
			when:    time.Unix(ts, 0),
			session: parts[3],
		})
	}
	slices.SortFunc(refs, func(a, b refInfo) int {
		return strings.Compare(b.id, a.id)
	})
	return refs, nil
}

// readEntry loads a checkpoint's meta.json, degrading to the subject-derived
// session id when the blob is missing or malformed.
func readEntry(repoDir string, r refInfo) Entry {
	e := Entry{Ref: r.ref, ID: r.id, CommitSHA: r.sha, When: r.when}
	e.Meta.SessionID = r.session
	if raw, err := gitIn(
		repoDir, nil, "show", r.sha+":meta.json",
	); err == nil {
		var m Meta
		if json.Unmarshal([]byte(raw), &m) == nil && m.SessionID != "" {
			e.Meta = m
		}
	}
	return e
}
