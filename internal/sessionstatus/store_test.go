package sessionstatus

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStatusActivityAndLabel(t *testing.T) {
	for _, s := range []Status{Working, Waiting, Idle} {
		if !s.Activity() {
			t.Errorf("%q should be an activity status", s)
		}
	}
	for _, s := range []Status{Exited, Unknown} {
		if s.Activity() {
			t.Errorf("%q should not be an activity status", s)
		}
	}
	if Unknown.Label() != "running" {
		t.Errorf("Unknown label = %q, want running", Unknown.Label())
	}
	if Exited.Label() != "exited" {
		t.Errorf("Exited label = %q, want exited", Exited.Label())
	}
}

func TestStoreRoundTrip(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	want := Record{Status: Waiting, Event: "Notification", UpdatedAt: now}

	if err := Write(home, "s1", want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, ok := Read(home, "s1")
	if !ok || got.Status != Waiting || got.Event != "Notification" ||
		!got.UpdatedAt.Equal(now) {
		t.Fatalf("round trip mismatch: got %+v ok=%v", got, ok)
	}
}

func TestStoreReadAbsentAndMalformed(t *testing.T) {
	home := t.TempDir()
	if _, ok := Read(home, "nope"); ok {
		t.Fatal("absent record read as present")
	}
	if err := os.MkdirAll(Dir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(Dir(home), "bad.json"),
		[]byte("{x"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if _, ok := Read(home, "bad"); ok {
		t.Fatal("malformed record read as present")
	}
	exited := `{"status":"exited","updatedAt":"2026-06-01T12:00:00Z"}`
	if err := os.WriteFile(
		filepath.Join(Dir(home), "e.json"),
		[]byte(exited),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if _, ok := Read(home, "e"); ok {
		t.Fatal("non-activity status read as a valid record")
	}
}

func TestStoreFresh(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if !(Record{Status: Idle, UpdatedAt: now.Add(-time.Minute)}).Fresh(now) {
		t.Fatal("one-minute-old record should be fresh")
	}
	if (Record{Status: Idle, UpdatedAt: now.Add(-Freshness - time.Second)}).Fresh(
		now,
	) {
		t.Fatal("record past the window should be stale")
	}
	if (Record{}).Fresh(now) {
		t.Fatal("zero record should never be fresh")
	}
}

func TestStoreRemove(t *testing.T) {
	home := t.TempDir()
	if err := Write(
		home,
		"s",
		Record{Status: Working, UpdatedAt: time.Now()},
	); err != nil {
		t.Fatal(err)
	}
	if err := Remove(home, "s"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := Read(home, "s"); ok {
		t.Fatal("record survived Remove")
	}
	if err := Remove(home, "s"); err != nil {
		t.Fatalf("Remove of absent record should be a no-op: %v", err)
	}
}

func TestDerivePrefersFreshHook(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	if got := Derive(home, "s", Working, now); got != Working {
		t.Fatalf("no record: got %q, want scraped Working", got)
	}

	_ = Write(home, "s", Record{Status: Waiting, UpdatedAt: now})
	if got := Derive(home, "s", Working, now); got != Waiting {
		t.Fatalf("fresh hook: got %q, want Waiting", got)
	}

	_ = Write(
		home,
		"s",
		Record{Status: Waiting, UpdatedAt: now.Add(-Freshness - time.Minute)},
	)
	if got := Derive(home, "s", Idle, now); got != Idle {
		t.Fatalf("stale hook: got %q, want scraped Idle", got)
	}
}
