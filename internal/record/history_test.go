package record

import (
	"strings"
	"testing"
)

func TestHistoryPreambleWithoutRef(t *testing.T) {
	dir := initRepo(t)
	if got := HistoryPreamble(dir, "main", "anything", 6000); got != "" {
		t.Errorf("no record should yield no preamble, got %q", got)
	}
}

func TestHistoryPreambleSameBranch(t *testing.T) {
	dir := initRepo(t)
	mustWrite(t, dir, Checkpoint{
		Meta: Meta{
			SessionID:   "same-a",
			Branch:      "task/history",
			WasaVersion: "t",
		},
		Intent: "wire up the widget loader",
	})
	mustWrite(t, dir, Checkpoint{
		Meta: Meta{
			SessionID:   "same-b",
			Branch:      "task/history",
			WasaVersion: "t",
		},
		Intent: "polish the widget styles",
	})
	mustWrite(t, dir, Checkpoint{
		Meta: Meta{
			SessionID:   "other-x",
			Branch:      "task/other",
			WasaVersion: "t",
		},
		Intent: "unrelated plumbing",
	})

	got := HistoryPreamble(dir, "task/history", "frobnicate the wobble", 6000)
	if !strings.Contains(got, "same-a") || !strings.Contains(got, "same-b") {
		t.Errorf("same-branch sessions missing from preamble:\n%s", got)
	}
	if strings.Contains(got, "other-x") {
		t.Errorf(
			"other-branch session leaked in without a keyword match:\n%s",
			got,
		)
	}
}

func TestHistoryPreambleKeywordOverlap(t *testing.T) {
	dir := initRepo(t)
	mustWrite(t, dir, Checkpoint{
		Meta:   Meta{SessionID: "snow", Branch: "main", WasaVersion: "t"},
		Intent: "we store ids as snowflakes never uuids",
	})
	mustWrite(t, dir, Checkpoint{
		Meta:   Meta{SessionID: "cache", Branch: "main", WasaVersion: "t"},
		Intent: "tidy up the config file",
	})

	got := HistoryPreamble(dir, "feature/x", "add a snowflakes column", 6000)
	if !strings.Contains(got, "snow") || !strings.Contains(got, "snowflakes") {
		t.Errorf("keyword-matching session missing from preamble:\n%s", got)
	}
	if strings.Contains(got, "cache") {
		t.Errorf("unrelated session selected on no keyword overlap:\n%s", got)
	}
}

func TestHistoryPreambleRespectsCap(t *testing.T) {
	dir := initRepo(t)
	for i := 'a'; i <= 't'; i++ {
		mustWrite(t, dir, Checkpoint{
			Meta: Meta{
				SessionID:   "bulk-" + string(i),
				Branch:      "bulk",
				WasaVersion: "t",
			},
			Intent: "session number " + string(i) + " doing bulk work here",
		})
	}
	const limit = 1200
	got := HistoryPreamble(dir, "bulk", "bulk work", limit)
	if got == "" {
		t.Fatal("expected a preamble from 20 sessions")
	}
	if len(got) > limit {
		t.Errorf("preamble %d bytes exceeds cap %d", len(got), limit)
	}
	if n := strings.Count(got, "── session"); n >= 20 {
		t.Errorf("cap did not truncate: %d sessions rendered", n)
	}
}

func TestHistoryPreambleOutcome(t *testing.T) {
	dir := initRepo(t)
	mustGit(
		t,
		dir,
		"commit",
		"-q",
		"--allow-empty",
		"-m",
		"teach the parser tabs",
	)
	sha := strings.TrimSpace(mustGit(t, dir, "rev-parse", "HEAD"))

	mustWrite(t, dir, Checkpoint{
		Meta: Meta{
			SessionID:   "with-commit",
			Branch:      "main",
			Commits:     []string{sha},
			WasaVersion: "t",
		},
		Intent: "make the parser handle tabs",
	})
	mustWrite(t, dir, Checkpoint{
		Meta:   Meta{SessionID: "no-commit", Branch: "main", WasaVersion: "t"},
		Intent: "just poked around the parser",
	})

	got := HistoryPreamble(dir, "feature/x", "parser tabs support", 6000)
	if !strings.Contains(got, "teach the parser tabs") {
		t.Errorf("commit subject not rendered in outcome:\n%s", got)
	}
	if !strings.Contains(got, "no commits recorded") {
		t.Errorf("commitless session should note no commits:\n%s", got)
	}
}
