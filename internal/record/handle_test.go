package record

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTranscript writes a minimal Claude transcript and returns its path.
func writeTranscript(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const sampleTranscript = `{"type":"user","message":{"role":"user",` +
	`"content":"build the feature"}}`

func TestHandleEventUnmanagedLifecycle(t *testing.T) {
	dir := initRepo(t)
	home := t.TempDir()
	transcript := writeTranscript(t, sampleTranscript)
	ev := Event{
		Agent:          "claude",
		AgentSessionID: "cc-uuid-1",
		TranscriptPath: transcript,
		Dir:            dir,
	}

	HandleEvent(home, ev)
	st, ok := loadState(home, "cc-uuid-1")
	if !ok {
		t.Fatal("no state after first event")
	}
	if !st.Unmanaged || st.Intent != "build the feature" ||
		st.Branch != "main" {
		t.Errorf("state = %+v", st)
	}
	if entries, _ := List(dir); len(entries) != 0 {
		t.Errorf("checkpoint written before any commit: %v", entries)
	}

	if err := os.WriteFile(
		filepath.Join(dir, "b.txt"), []byte("b\n"), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "b.txt")
	mustGit(t, dir, "commit", "-q", "-m", "add b")
	head := mustGit(t, dir, "rev-parse", "HEAD")

	HandleEvent(home, ev)
	entries, err := List(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("List after commit = %v, %v", entries, err)
	}
	m := entries[0].Meta
	if m.Commit != head || len(m.Commits) != 1 || m.Commits[0] != head {
		t.Errorf("commit-linked meta = %+v, head %s", m, head)
	}
	if !m.Unmanaged || m.SessionID != "cc-uuid-1" {
		t.Errorf("meta = %+v", m)
	}

	ev.End = true
	HandleEvent(home, ev)
	entries, _ = List(dir)
	if len(entries) != 1 {
		t.Fatalf("List after end = %v", entries)
	}
	if entries[0].Meta.FinishedAt.IsZero() {
		t.Error("closing checkpoint has no finishedAt")
	}
	if _, ok := loadState(home, "cc-uuid-1"); ok {
		t.Error("state survived SessionEnd")
	}
}

func TestHandleEventPromptBecomesIntent(t *testing.T) {
	dir := initRepo(t)
	home := t.TempDir()

	HandleEvent(home, Event{
		Agent:          "gemini",
		AgentSessionID: "gem-1",
		Prompt:         "refactor the parser",
		Dir:            dir,
	})
	st, ok := loadState(home, "gem-1")
	if !ok || st.Intent != "refactor the parser" || st.Agent != "gemini" {
		t.Errorf("state = %+v, %v", st, ok)
	}

	HandleEvent(home, Event{
		Agent:          "gemini",
		AgentSessionID: "gem-1",
		Prompt:         "a later prompt",
		Dir:            dir,
		End:            true,
	})
	e, intent, _, err := Find(dir, "gem-1")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if intent != "refactor the parser" {
		t.Errorf("intent = %q, want the first prompt", intent)
	}
	if e.Meta.Agent != "gemini" || !e.Meta.Unmanaged {
		t.Errorf("meta = %+v", e.Meta)
	}
}

func TestHandleEventUnknownToolIsNoOp(t *testing.T) {
	home := t.TempDir()
	HandleEvent(home, Event{
		Agent: "not-an-agent", AgentSessionID: "x", Dir: initRepo(t),
	})
	if _, ok := loadState(home, "x"); ok {
		t.Error("state written for an unsupported tool")
	}
}

func TestHandleEventOutsideRepoIsNoOp(t *testing.T) {
	home := t.TempDir()
	HandleEvent(home, Event{
		Agent: "claude", AgentSessionID: "x", Dir: t.TempDir(),
	})
	if _, ok := loadState(home, "x"); ok {
		t.Error("state written outside a repository")
	}
}

func TestFinishManagedSession(t *testing.T) {
	dir := initRepo(t)
	home := t.TempDir()
	base := mustGit(t, dir, "rev-parse", "HEAD")

	wt := filepath.Join(t.TempDir(), "wt")
	mustGit(t, dir, "worktree", "add", "-q", "-b", "task/z", wt)
	if err := os.WriteFile(
		filepath.Join(wt, "c.txt"), []byte("c\n"), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	mustGit(t, wt, "add", "c.txt")
	mustGit(t, wt, "commit", "-q", "-m", "agent work")
	agentCommit := mustGit(t, wt, "rev-parse", "HEAD")

	transcript := writeTranscript(t, sampleTranscript)
	HandleEvent(home, Event{
		Agent:          "claude",
		AgentSessionID: "ignored", TranscriptPath: transcript,
		Dir: wt, WasaSession: "wasa01",
	})

	err := Finish(home, FinishInfo{
		SessionID: "wasa01", WorkspaceID: "ws1", Program: "claude",
		Branch: "task/z", BaseCommit: base, RepoDir: dir,
		StartedAt: time.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}

	e, intent, _, err := Find(dir, "wasa01")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	m := e.Meta
	if m.FinishedAt.IsZero() || m.Unmanaged || m.WorkspaceID != "ws1" ||
		m.Agent != "claude" || m.Branch != "task/z" {
		t.Errorf("closing meta = %+v", m)
	}
	if len(m.Commits) != 1 || m.Commits[0] != agentCommit {
		t.Errorf("commits = %v, want [%s]", m.Commits, agentCommit)
	}
	if intent != "build the feature" {
		t.Errorf("intent = %q", intent)
	}
	if _, ok := loadState(home, "wasa01"); ok {
		t.Error("state survived Finish")
	}
}

func TestFinishWithoutHookDataStillRecords(t *testing.T) {
	dir := initRepo(t)
	home := t.TempDir()

	err := Finish(home, FinishInfo{
		SessionID: "lonely", Program: "claude", RepoDir: dir,
		Branch: "main",
	})
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	e, _, _, err := Find(dir, "lonely")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(e.Meta.Gaps) == 0 {
		t.Errorf("degraded checkpoint notes no gaps: %+v", e.Meta)
	}
}

func TestFinishOutsideRepoIsNil(t *testing.T) {
	if err := Finish(t.TempDir(), FinishInfo{SessionID: "s"}); err != nil {
		t.Errorf("Finish with nothing known = %v, want nil", err)
	}
}
