package record

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// placeTranscript writes a transcript into the Claude Code slug directory for
// repoDir, so Import discovers it the way it would a real session.
func placeTranscript(t *testing.T, claudeDir, repoDir, sessionID, body string) {
	t.Helper()
	dir := filepath.Join(claudeDir, "projects", sanitizePath(repoDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, sessionID+".jsonl"), []byte(body), 0o644,
	); err != nil {
		t.Fatal(err)
	}
}

const goodSession = `{"type":"user","timestamp":"2026-01-02T10:00:00Z",` +
	`"gitBranch":"main","sessionId":"sess-good","message":{"role":"user",` +
	`"content":"<ide_selection>x.go:1</ide_selection>fix the parser with ` +
	`ghp_abcdefghijklmnopqrstuvwxyz0123456789"}}
{"type":"assistant","timestamp":"2026-01-02T10:05:00Z","message":` +
	`{"role":"assistant","content":[{"type":"text","text":"done"}]}}`

// a valid first line then a truncated second line (invalid JSON).
const truncatedSession = `{"type":"user","timestamp":"2026-01-02T11:00:00Z",` +
	`"message":{"role":"user","content":"another task"}}
{"type":"assistant","timestamp":"2026-01-02T11:0`

func TestImport(t *testing.T) {
	dir := initRepo(t)
	claude := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	placeTranscript(t, claude, dir, "sess-good", goodSession)
	placeTranscript(t, claude, dir, "sess-bad", truncatedSession)

	dry, err := Import(dir, "", true)
	if err != nil {
		t.Fatalf("dry-run Import: %v", err)
	}
	if len(dry.Imported) != 1 || dry.Failed != 1 || dry.Skipped != 0 {
		t.Fatalf("dry run = imported %d, failed %d, skipped %d; want 1,1,0",
			len(dry.Imported), dry.Failed, dry.Skipped)
	}
	if entries, _ := List(dir); len(entries) != 0 {
		t.Fatalf("dry run wrote %d refs, want 0", len(entries))
	}

	res, err := Import(dir, "", false)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(res.Imported) != 1 || res.Failed != 1 {
		t.Fatalf("import = imported %d, failed %d; want 1,1",
			len(res.Imported), res.Failed)
	}
	if len(res.Warnings) != 1 {
		t.Fatalf(
			"want one warning for the truncated file, got %v",
			res.Warnings,
		)
	}

	entries, err := List(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("List = %v, %v; want one entry", entries, err)
	}
	m := entries[0].Meta
	if !m.Imported || !m.Unmanaged || m.WorkspaceID != "" {
		t.Errorf("meta not marked imported/unmanaged/no-workspace: %+v", m)
	}
	if m.SessionID != "sess-good" || m.AgentSessionID != "sess-good" {
		t.Errorf("session ids = %q / %q", m.SessionID, m.AgentSessionID)
	}
	want := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	if !m.StartedAt.Equal(want) {
		t.Errorf("StartedAt = %v, want %v", m.StartedAt, want)
	}

	_, intent, transcript, err := Find(dir, "sess-good")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !strings.Contains(intent, "fix the parser") ||
		strings.Contains(intent, "ide_selection") {
		t.Errorf("intent not sanitized: %q", intent)
	}
	if strings.Contains(intent, "ghp_") ||
		!strings.Contains(intent, placeholder) {
		t.Errorf("intent not redacted: %q", intent)
	}
	if strings.Contains(string(transcript), "ghp_") {
		t.Errorf("transcript not redacted: %q", transcript)
	}

	again, err := Import(dir, "", false)
	if err != nil {
		t.Fatalf("re-Import: %v", err)
	}
	if len(again.Imported) != 0 || again.Skipped != 1 {
		t.Errorf("re-run = imported %d, skipped %d; want 0,1",
			len(again.Imported), again.Skipped)
	}
	if entries, _ := List(dir); len(entries) != 1 {
		t.Errorf("re-run changed ref count to %d, want 1", len(entries))
	}
}

func TestImportFromDir(t *testing.T) {
	dir := initRepo(t)
	from := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(from, "sess-x.jsonl"), []byte(goodSession), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	res, err := Import(dir, from, false)
	if err != nil || len(res.Imported) != 1 {
		t.Fatalf("Import --from = %+v, %v", res, err)
	}
}

func TestCommitsInWindow(t *testing.T) {
	base := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	commits := []commitTime{
		{sha: "c3", when: base.Add(3 * time.Hour)},
		{sha: "c2", when: base.Add(2 * time.Hour)},
		{sha: "c1", when: base.Add(1 * time.Hour)},
	}
	got := commitsInWindow(
		commits, base.Add(time.Hour), base.Add(2*time.Hour),
	)
	if strings.Join(got, ",") != "c1,c2" {
		t.Errorf("commitsInWindow = %v, want [c1 c2] oldest-first", got)
	}
	out := commitsInWindow(commits, base, base.Add(30*time.Minute))
	if out != nil {
		t.Errorf("empty window = %v, want nil", out)
	}
}

func TestOverlapsAny(t *testing.T) {
	base := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	win := func(s, e int) ImportCandidate {
		return ImportCandidate{Meta: Meta{
			StartedAt:  base.Add(time.Duration(s) * time.Hour),
			FinishedAt: base.Add(time.Duration(e) * time.Hour),
		}}
	}
	overlapping := []ImportCandidate{win(0, 2), win(1, 3)}
	if !overlapsAny(overlapping, 0) || !overlapsAny(overlapping, 1) {
		t.Error("overlapping windows should report overlap")
	}
	disjoint := []ImportCandidate{win(0, 1), win(2, 3)}
	if overlapsAny(disjoint, 0) || overlapsAny(disjoint, 1) {
		t.Error("disjoint windows should not report overlap")
	}
}
