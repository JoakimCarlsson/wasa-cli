package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/joakimcarlsson/wasa/internal/hook"
	"github.com/joakimcarlsson/wasa/internal/hookstatus"
)

func feedStdin(t *testing.T, content string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdin-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() {
		os.Stdin = old
		f.Close()
	})
}

func TestHookHandlerWritesRecord(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WASA_HOME", home)
	t.Setenv(hook.EnvSession, "sx")
	feedStdin(t, `{"hook_event_name":"Notification","session_id":"abc"}`)

	if err := runHookHandler(nil); err != nil {
		t.Fatalf("runHookHandler: %v", err)
	}
	rec, ok := hookstatus.Read(home, "sx")
	if !ok {
		t.Fatal("handler wrote no record")
	}
	if rec.Status != hookstatus.StatusWaiting {
		t.Fatalf("status = %q, want waiting", rec.Status)
	}
}

func TestHookHandlerNoSessionIsNoop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WASA_HOME", home)
	t.Setenv(hook.EnvSession, "")
	feedStdin(t, `{"hook_event_name":"Stop"}`)

	if err := runHookHandler(nil); err != nil {
		t.Fatalf("runHookHandler: %v", err)
	}
	if entries, _ := os.ReadDir(
		filepath.Join(home, "hooks"),
	); len(
		entries,
	) != 0 {
		t.Fatal("handler wrote a record with no session id")
	}
}

func TestHookHandlerUnmappedEventIsNoop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WASA_HOME", home)
	t.Setenv(hook.EnvSession, "sy")
	feedStdin(t, `{"hook_event_name":"PreCompact"}`)

	if err := runHookHandler(nil); err != nil {
		t.Fatalf("runHookHandler: %v", err)
	}
	if _, ok := hookstatus.Read(home, "sy"); ok {
		t.Fatal("handler wrote a record for an unmapped event")
	}
}
