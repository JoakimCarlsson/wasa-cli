package hookstatus

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteReadRoundTrip(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	want := Record{Status: StatusWaiting, Event: "Notification", UpdatedAt: now}

	if err := Write(home, "sess1", want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, ok := Read(home, "sess1")
	if !ok {
		t.Fatal("Read reported no record after Write")
	}
	if got.Status != want.Status || got.Event != want.Event ||
		!got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, want)
	}
}

func TestReadAbsentIsNotOK(t *testing.T) {
	if _, ok := Read(t.TempDir(), "nope"); ok {
		t.Fatal("Read reported a record for an absent session")
	}
}

func TestReadMalformedIsNotOK(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(Dir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(Dir(home), "bad.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := Read(home, "bad"); ok {
		t.Fatal("malformed record was treated as present")
	}
}

func TestReadInvalidStatusIsNotOK(t *testing.T) {
	home := t.TempDir()
	if err := Write(
		home,
		"s",
		Record{Status: "bogus", UpdatedAt: time.Now()},
	); err == nil {
		_, _ = Read(home, "s")
	}
	if err := os.MkdirAll(Dir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(Dir(home), "s.json")
	if err := os.WriteFile(
		path,
		[]byte(`{"status":"bogus","updatedAt":"2026-06-01T12:00:00Z"}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if _, ok := Read(home, "s"); ok {
		t.Fatal("record with an unrecognised status was treated as valid")
	}
}

func TestFresh(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	fresh := Record{Status: StatusIdle, UpdatedAt: now.Add(-time.Minute)}
	if !fresh.Fresh(now) {
		t.Fatal("a one-minute-old record should be fresh")
	}
	stale := Record{
		Status:    StatusIdle,
		UpdatedAt: now.Add(-Freshness - time.Second),
	}
	if stale.Fresh(now) {
		t.Fatal("a record older than the freshness window should be stale")
	}
	var zero Record
	if zero.Fresh(now) {
		t.Fatal("a zero record should never be fresh")
	}
}

func TestRemove(t *testing.T) {
	home := t.TempDir()
	if err := Write(
		home,
		"s",
		Record{Status: StatusWorking, UpdatedAt: time.Now()},
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
