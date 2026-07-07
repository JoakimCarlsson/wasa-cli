package record

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildPreambleIncludesIntentCommitsAndTail(t *testing.T) {
	transcript := strings.Join([]string{
		`{"type":"user","message":{"content":"the codename is KORVSUP"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"noted"}]}}`,
		`{"type":"user","isMeta":true,"message":{"content":"meta skip"}}`,
	}, "\n")

	got := BuildPreamble(
		"add a resume command",
		Meta{Commits: []string{"abcdef1234567890", "0011223344556677"}},
		[]byte(transcript),
	)

	for _, want := range []string{
		"resuming an earlier session",
		"add a resume command",
		"Commits already produced (2)",
		"abcdef123456",
		"the codename is KORVSUP",
		"noted",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("preamble missing %q\n---\n%s", want, got)
		}
	}
	if strings.Contains(got, "meta skip") {
		t.Errorf("preamble should skip meta lines:\n%s", got)
	}
}

func TestBuildPreambleWithoutTranscriptStillHasIntent(t *testing.T) {
	got := BuildPreamble("do the thing", Meta{}, nil)
	if !strings.Contains(got, "do the thing") {
		t.Errorf("preamble missing intent:\n%s", got)
	}
	if strings.Contains(got, "Recent conversation") {
		t.Errorf("empty transcript should not add a conversation section:\n%s", got)
	}
}

func TestResumeArgsPerAgent(t *testing.T) {
	cases := []struct {
		program string
		id      string
		want    []string
		ok      bool
	}{
		{"claude", "s1", []string{"--resume", "s1"}, true},
		{"/usr/bin/claude --dangerously", "s1", []string{"--resume", "s1"}, true},
		{"gemini", "s2", []string{"--resume", "s2"}, true},
		{"codex", "s3", []string{"resume", "s3"}, true},
		{"copilot", "s4", []string{"--resume", "s4"}, true},
		{"cursor-agent", "s5", nil, false},
		{"claude", "", nil, false},
		{"unknown-agent", "s6", nil, false},
	}
	for _, c := range cases {
		got, ok := ResumeArgs(c.program, c.id)
		if ok != c.ok {
			t.Errorf("ResumeArgs(%q,%q) ok=%v want %v", c.program, c.id, ok, c.ok)
			continue
		}
		if !equalStrings(got, c.want) {
			t.Errorf("ResumeArgs(%q,%q)=%v want %v", c.program, c.id, got, c.want)
		}
	}
}

func TestRestoreTranscriptThenLocalTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	worktree := filepath.Join(t.TempDir(), "wt")

	if got := LocalTranscript("claude", "sess1", worktree); got != "" {
		t.Fatalf("expected no local transcript yet, got %q", got)
	}

	data := []byte(`{"type":"user","message":{"content":"hi"}}` + "\n")
	if err := RestoreTranscript("claude", "sess1", worktree, data); err != nil {
		t.Fatalf("RestoreTranscript: %v", err)
	}

	path := LocalTranscript("claude", "sess1", worktree)
	if path == "" {
		t.Fatalf("restored transcript not found by LocalTranscript")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read restored transcript: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("restored transcript = %q want %q", got, data)
	}
}

func TestRestoreTranscriptUnsupportedAgent(t *testing.T) {
	if err := RestoreTranscript(
		"cursor-agent", "s1", t.TempDir(), []byte("x"),
	); err == nil {
		t.Error("expected error restoring for an agent with no transcript target")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
