package record

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ImportCandidate is one session that import would write: everything derived
// from its transcript, before (or without) touching the store. Ref is set
// only after a successful write.
type ImportCandidate struct {
	SessionID string
	Path      string
	Meta      Meta
	Intent    string
	Ref       string
}

// ImportResult reports one import run.
type ImportResult struct {
	// Imported is the sessions written (or, on a dry run, that would be).
	Imported []ImportCandidate
	// Skipped is how many sessions were already in the store.
	Skipped int
	// Failed is how many transcript files were malformed and skipped.
	Failed int
	// Warnings is one line per malformed/unreadable file, for the caller to
	// print.
	Warnings []string
}

// Import backfills the checkpoint store from the current repo's pre-existing
// Claude Code transcripts. It locates transcripts under
// ~/.claude/projects/<slug>/ for repoDir and its worktrees (or under fromDir
// when set), converts each unseen session into one checkpoint through the
// normal Write pipeline (redaction included), and reports what it did. A
// session whose id is already recorded is skipped; a malformed or truncated
// transcript is skipped with a warning, never a failed run. On a dry run
// nothing is written.
func Import(repoDir, fromDir string, dryRun bool) (ImportResult, error) {
	var res ImportResult

	existing, err := seenSessions(repoDir)
	if err != nil {
		return res, err
	}

	files, err := transcriptFiles(repoDir, fromDir)
	if err != nil {
		return res, err
	}

	seen := map[string]bool{}
	var cands []ImportCandidate
	for _, path := range files {
		sid := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		if seen[sid] {
			continue
		}
		seen[sid] = true
		if existing[sid] {
			res.Skipped++
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			res.Failed++
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("skipped %s: %v", path, err))
			continue
		}
		info, err := scanTranscript(data)
		if err != nil {
			res.Failed++
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("skipped %s: malformed transcript (%v)", path, err))
			continue
		}
		cands = append(cands, ImportCandidate{
			SessionID: sid,
			Path:      path,
			Meta:      importMeta(sid, info),
			Intent:    FirstUserMessage(data),
		})
	}

	linkCommits(repoDir, cands)

	if dryRun {
		res.Imported = cands
		return res, nil
	}

	var refs []string
	for i := range cands {
		c := &cands[i]
		data, err := os.ReadFile(c.Path)
		if err != nil {
			res.Failed++
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("skipped %s: %v", c.Path, err))
			continue
		}
		ref, err := Write(repoDir, Checkpoint{
			Meta:       c.Meta,
			Intent:     c.Intent,
			Transcript: data,
			Timestamp:  c.Meta.FinishedAt,
		})
		if err != nil {
			res.Failed++
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("skipped %s: %v", c.Path, err))
			continue
		}
		c.Ref = ref
		refs = append(refs, ref)
		res.Imported = append(res.Imported, *c)
	}
	_ = Push(repoDir, refs...)
	return res, nil
}

// seenSessions is the set of session ids already in the store, the idempotency
// key: import writes SessionID == the transcript's session id and List dedups
// on SessionID, so a re-run skips everything it wrote before.
func seenSessions(repoDir string) (map[string]bool, error) {
	entries, err := List(repoDir)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(entries))
	for _, e := range entries {
		set[e.Meta.SessionID] = true
	}
	return set, nil
}

// transcriptFiles lists the flat <session>.jsonl transcripts for the repo.
// With fromDir set it globs that one directory; otherwise it maps repoDir and
// every linked worktree through the Claude Code slug into
// ~/.claude/projects/<slug>/. Sidechain subdirectories (<session>/...) are not
// matched by the top-level glob and so are ignored.
func transcriptFiles(repoDir, fromDir string) ([]string, error) {
	var dirs []string
	if fromDir != "" {
		dirs = []string{fromDir}
	} else {
		base := filepath.Join(
			agentHome("CLAUDE_CONFIG_DIR", ".claude"), "projects",
		)
		for _, d := range append([]string{repoDir}, worktreePaths(repoDir)...) {
			dirs = append(dirs, filepath.Join(base, sanitizePath(d)))
		}
	}
	var files []string
	for _, d := range dirs {
		m, err := filepath.Glob(filepath.Join(d, "*.jsonl"))
		if err != nil {
			return nil, err
		}
		files = append(files, m...)
	}
	return files, nil
}

// worktreePaths lists the repo's linked worktree directories (main checkout
// excluded — it is passed separately). Best-effort: no worktrees, or a git
// that cannot answer, yields nothing.
func worktreePaths(repoDir string) []string {
	out, err := gitIn(repoDir, nil, "worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		p, ok := strings.CutPrefix(strings.TrimSpace(line), "worktree ")
		if !ok {
			continue
		}
		if abs, err := filepath.Abs(repoDir); err != nil || p != abs {
			paths = append(paths, p)
		}
	}
	return paths
}

// transcriptInfo is the metadata a transcript scan yields: the session's time
// window and the branch it ran on.
type transcriptInfo struct {
	start  time.Time
	end    time.Time
	branch string
}

// scanTranscript validates a transcript and extracts its time window and
// branch in one pass. Every non-empty line must be valid JSON; a line that is
// not marks a truncated or corrupt file and errors, so the caller skips it
// with a warning rather than importing half a session.
func scanTranscript(data []byte) (transcriptInfo, error) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), maxTranscriptLine)
	var info transcriptInfo
	for sc.Scan() {
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		var line struct {
			Timestamp string `json:"timestamp"`
			GitBranch string `json:"gitBranch"`
		}
		if err := json.Unmarshal(raw, &line); err != nil {
			return transcriptInfo{}, err
		}
		if line.GitBranch != "" && line.GitBranch != "HEAD" &&
			info.branch == "" {
			info.branch = line.GitBranch
		}
		if line.Timestamp == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, line.Timestamp)
		if err != nil {
			continue
		}
		if info.start.IsZero() || t.Before(info.start) {
			info.start = t
		}
		if t.After(info.end) {
			info.end = t
		}
	}
	if err := sc.Err(); err != nil {
		return transcriptInfo{}, err
	}
	return info, nil
}

// importMeta builds the checkpoint metadata for a backfilled session: marked
// imported and unmanaged, with no workspace, session id doubling as the
// agent's own id, and the transcript's time window. Missing pieces are noted
// as gaps so the record stays honest.
func importMeta(sessionID string, info transcriptInfo) Meta {
	m := Meta{
		SessionID:      sessionID,
		AgentSessionID: sessionID,
		Agent:          "claude",
		Branch:         info.branch,
		StartedAt:      info.start,
		FinishedAt:     info.end,
		Unmanaged:      true,
		Imported:       true,
	}
	if info.start.IsZero() || info.end.IsZero() {
		m.Gaps = append(m.Gaps, "transcript timestamps unavailable")
	}
	return m
}

// linkCommits attaches commit SHAs to each candidate by time-window
// correlation against git log — best-effort and honest, per the issue's "never
// guess a wrong SHA". A candidate is linked only when its window is known and
// does not overlap another candidate's window; the links are always gap-noted
// as inferred. An ambiguous (overlapping) or unknown window is left unlinked
// with a noted gap instead.
func linkCommits(repoDir string, cands []ImportCandidate) {
	if len(cands) == 0 {
		return
	}
	commits := repoCommits(repoDir)
	for i := range cands {
		m := &cands[i].Meta
		if m.StartedAt.IsZero() || m.FinishedAt.IsZero() {
			m.Gaps = append(m.Gaps,
				"commit links omitted: session window unknown")
			continue
		}
		if overlapsAny(cands, i) {
			m.Gaps = append(m.Gaps,
				"commit links omitted: session window ambiguous")
			continue
		}
		shas := commitsInWindow(commits, m.StartedAt, m.FinishedAt)
		if len(shas) == 0 {
			continue
		}
		m.Commits = shas
		m.Gaps = append(m.Gaps, "commits inferred by time window")
	}
}

// overlapsAny reports whether candidate i's time window intersects any other
// candidate's window. Two windows overlap when each starts no later than the
// other ends.
func overlapsAny(cands []ImportCandidate, i int) bool {
	a := cands[i].Meta
	for j := range cands {
		if j == i {
			continue
		}
		b := cands[j].Meta
		if b.StartedAt.IsZero() || b.FinishedAt.IsZero() {
			continue
		}
		if !a.StartedAt.After(b.FinishedAt) &&
			!b.StartedAt.After(a.FinishedAt) {
			return true
		}
	}
	return false
}

// commitTime pairs a commit SHA with its committer time.
type commitTime struct {
	sha  string
	when time.Time
}

// repoCommits lists every commit reachable from any ref with its committer
// time, so windows can be matched in-process. A repo git cannot read yields
// nothing.
func repoCommits(repoDir string) []commitTime {
	out, err := gitIn(repoDir, nil, "log", "--all", "--format=%H%x1f%cI")
	if err != nil || out == "" {
		return nil
	}
	var cs []commitTime
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "\x1f", 2)
		if len(parts) != 2 {
			continue
		}
		t, err := time.Parse(time.RFC3339, parts[1])
		if err != nil {
			continue
		}
		cs = append(cs, commitTime{sha: parts[0], when: t})
	}
	return cs
}

// commitsInWindow returns the SHAs whose committer time falls within
// [start, end], oldest first — repoCommits is newest-first, so it walks back.
func commitsInWindow(commits []commitTime, start, end time.Time) []string {
	var shas []string
	for i := len(commits) - 1; i >= 0; i-- {
		c := commits[i]
		if c.when.Before(start) || c.when.After(end) {
			continue
		}
		shas = append(shas, c.sha)
	}
	return shas
}
