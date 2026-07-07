package record

import (
	"errors"
	"testing"
	"time"
)

func TestSearch(t *testing.T) {
	dir := initRepo(t)

	mustWrite(t, dir, Checkpoint{
		Meta: Meta{
			SessionID:   "sess1",
			Branch:      "task/retry",
			WasaVersion: "t",
		},
		Intent: "old note about retry logic",
	})
	mustWrite(t, dir, Checkpoint{
		Meta: Meta{
			SessionID:   "sess1",
			Branch:      "task/retry",
			WasaVersion: "t",
		},
		Intent: "rework the retry logic with backoff",
	})
	mustWrite(t, dir, Checkpoint{
		Meta: Meta{
			SessionID:   "sess2",
			Branch:      "task/cache",
			WasaVersion: "t",
		},
		Intent: "tidy up config",
		Transcript: []byte(
			`{"role":"assistant","text":"let us drop the cache here"}`,
		),
	})

	hits, err := Search(dir, SearchOpts{Query: "retry logic"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("retry logic matched %d sessions, want 1 (deduped)", len(hits))
	}
	if hits[0].Meta.SessionID != "sess1" || hits[0].File != "intent" {
		t.Errorf(
			"hit = %s/%s, want sess1/intent",
			hits[0].Meta.SessionID,
			hits[0].File,
		)
	}
	if hits[0].LineText != "rework the retry logic with backoff" {
		t.Errorf(
			"snippet from wrong (not newest) checkpoint: %q",
			hits[0].LineText,
		)
	}
	if got := hits[0].LineText[hits[0].Start:hits[0].End]; got != "retry logic" {
		t.Errorf("match span = %q, want %q", got, "retry logic")
	}

	if h, _ := Search(dir, SearchOpts{Query: "RETRY LOGIC"}); len(h) != 1 {
		t.Errorf("case-insensitive search matched %d, want 1", len(h))
	}

	trans, err := Search(dir, SearchOpts{Query: "drop the cache"})
	if err != nil {
		t.Fatalf("Search transcript: %v", err)
	}
	if len(trans) != 1 || trans[0].Meta.SessionID != "sess2" ||
		trans[0].File != "transcript" {
		t.Fatalf("transcript search = %+v, want sess2/transcript", trans)
	}

	if h, _ := Search(
		dir,
		SearchOpts{Query: "drop the cache", IntentOnly: true},
	); len(
		h,
	) != 0 {
		t.Errorf("intent-only matched transcript phrase (%d hits)", len(h))
	}

	if h, _ := Search(
		dir,
		SearchOpts{Query: "e", Branch: "task/retry"},
	); len(h) != 1 ||
		h[0].Meta.SessionID != "sess1" {
		t.Errorf("branch filter = %+v, want just sess1", h)
	}

	if h, _ := Search(dir, SearchOpts{Query: "e", Limit: 1}); len(h) != 1 {
		t.Errorf("limit 1 returned %d sessions", len(h))
	}

	if h, _ := Search(
		dir,
		SearchOpts{Query: "retry.*backoff", Regex: true},
	); len(
		h,
	) != 1 {
		t.Errorf("regex search matched %d, want 1", len(h))
	}

	future := time.Now().Add(time.Hour)
	if h, _ := Search(dir, SearchOpts{Query: "e", Since: future}); len(h) != 0 {
		t.Errorf("since=future matched %d, want 0", len(h))
	}
	past := time.Now().Add(-time.Hour)
	if h, _ := Search(
		dir,
		SearchOpts{Query: "retry logic", Since: past},
	); len(
		h,
	) != 1 {
		t.Errorf("since=past matched %d, want 1", len(h))
	}

	if h, err := Search(
		dir,
		SearchOpts{Query: "no-such-string-xyz"},
	); err != nil ||
		len(h) != 0 {
		t.Errorf("no-match search = %d hits, %v; want 0, nil", len(h), err)
	}
}

func TestSearchWithoutRef(t *testing.T) {
	dir := initRepo(t)
	if _, err := Search(
		dir,
		SearchOpts{Query: "anything"},
	); !errors.Is(
		err,
		ErrNoRecord,
	) {
		t.Errorf("Search without a record = %v, want ErrNoRecord", err)
	}
}

func TestSearchBadRegex(t *testing.T) {
	dir := initRepo(t)
	mustWrite(t, dir, Checkpoint{
		Meta: Meta{SessionID: "s", WasaVersion: "t"}, Intent: "x",
	})
	if _, err := Search(dir, SearchOpts{Query: "(", Regex: true}); err == nil {
		t.Error("invalid regex should error")
	}
}
