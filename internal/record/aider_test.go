package record

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleAiderHistory = `
# aider chat started at 2026-01-02 10:00:00

#### build the feature

Sure, I'll build the feature.

I made these changes:

` + "```python\ndef foo():\n    pass\n```" + `

> Applied edit to foo.py

#### now add a test

Added a test for foo.
`

func TestAiderRecorderNoHookMechanism(t *testing.T) {
	dir := t.TempDir()
	r := aiderRecorder{}

	if err := r.InstallHooks(dir, "/usr/bin/wasa"); err != nil {
		t.Errorf("InstallHooks = %v, want nil (no-op)", err)
	}
	if err := r.RemoveHooks(dir); err != nil {
		t.Errorf("RemoveHooks = %v, want nil (no-op)", err)
	}
	if r.HooksInstalled(dir) {
		t.Error("HooksInstalled = true, want false (nothing to detect)")
	}
	if got := r.TranscriptTarget("sid", dir); got != "" {
		t.Errorf("TranscriptTarget = %q, want \"\" (not session-keyed)", got)
	}
	if got := r.ResumeArgs("sid"); got != nil {
		t.Errorf("ResumeArgs = %v, want nil (no native resume)", got)
	}
}

func TestAiderRecorderLocateTranscript(t *testing.T) {
	dir := t.TempDir()
	r := aiderRecorder{}

	if got := r.LocateTranscript("", dir); got != "" {
		t.Errorf("LocateTranscript before write = %q, want \"\"", got)
	}

	path := filepath.Join(dir, ".aider.chat.history.md")
	if err := os.WriteFile(
		path,
		[]byte(sampleAiderHistory),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if got := r.LocateTranscript("", dir); got != path {
		t.Errorf("LocateTranscript = %q, want %q", got, path)
	}
}

func TestAiderNormalizeAndIntent(t *testing.T) {
	r := aiderRecorder{}
	msgs := r.Normalize([]byte(sampleAiderHistory))

	var users, assistants []string
	for _, m := range msgs {
		switch m.Role {
		case "user":
			users = append(users, m.Content)
		case "assistant":
			assistants = append(assistants, m.Content)
		default:
			t.Errorf("message with unexpected role %q: %+v", m.Role, m)
		}
	}

	if len(users) != 2 || users[0] != "build the feature" ||
		users[1] != "now add a test" {
		t.Errorf("user messages = %v", users)
	}
	if len(assistants) != 2 {
		t.Errorf("assistant messages = %v, want 2", assistants)
	}

	if intent := r.Intent(
		[]byte(sampleAiderHistory),
	); intent != "build the feature" {
		t.Errorf("Intent = %q, want %q", intent, "build the feature")
	}
}

// TestFinishAiderPicksUpChatLogWithoutHooks checks the Finish fallback: an
// Aider session that never fired a hook (it has none) still gets its
// transcript and intent from .aider.chat.history.md, so it is recorded with
// "no hook data received" but not "transcript unavailable".
func TestFinishAiderPicksUpChatLogWithoutHooks(t *testing.T) {
	dir := initRepo(t)
	home := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, ".aider.chat.history.md"),
		[]byte(sampleAiderHistory), 0o644,
	); err != nil {
		t.Fatal(err)
	}

	if err := Finish(home, FinishInfo{
		SessionID: "aider01", Program: "aider", RepoDir: dir, Branch: "main",
	}); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	e, intent, transcript, err := Find(dir, "aider01")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if intent != "build the feature" {
		t.Errorf("intent = %q, want %q", intent, "build the feature")
	}
	if len(transcript) == 0 {
		t.Error("transcript empty, want the picked-up chat log")
	}
	for _, gap := range e.Meta.Gaps {
		if gap == "transcript unavailable" {
			t.Errorf(
				"gaps = %v, want no \"transcript unavailable\"",
				e.Meta.Gaps,
			)
		}
	}
	if !containsGap(e.Meta.Gaps, "no hook data received") {
		t.Errorf("gaps = %v, want \"no hook data received\"", e.Meta.Gaps)
	}
}

func containsGap(gaps []string, want string) bool {
	for _, g := range gaps {
		if g == want {
			return true
		}
	}
	return false
}
