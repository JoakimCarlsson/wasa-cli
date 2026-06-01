package tui

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/joakimcarlsson/wasa/internal/config"
	"github.com/joakimcarlsson/wasa/internal/registry"
)

// captureBackend is a SessionBackend whose pane content and liveness the test
// drives directly, so a session can be made to settle at a prompt or to vanish
// from tmux on command. It does not stream, so the cockpit derives status from
// the one-shot Capture path.
type captureBackend struct {
	panes map[string]string
	alive map[string]bool
}

func (b *captureBackend) SpawnEnv(string, string, []string, ...string) error {
	return nil
}

func (b *captureBackend) AttachCmd(
	string,
) (*exec.Cmd, error) {
	return nil, nil
}
func (b *captureBackend) Capture(name string) (string, error) {
	return b.panes[name], nil
}

func (b *captureBackend) Has(
	name string,
) (bool, error) {
	return b.alive[name], nil
}
func (b *captureBackend) List() ([]string, error) { return nil, nil }
func (b *captureBackend) Kill(string) error       { return nil }

type notifRec struct{ title, body string }

// notifyModel builds a model over two running sessions, s1 (selected/focused)
// and s2, wired to a capture backend and a controllable clock, recording every
// notification it would fire.
func notifyModel(
	t *testing.T,
) (Model, *captureBackend, *fakeClock, *[]notifRec) {
	t.Helper()
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	reg.AddSession(&registry.Session{
		ID: "s1", WorkspaceID: ws.ID, Branch: "feat/s1",
		Status: registry.StatusRunning, TmuxName: "t1",
	})
	reg.AddSession(&registry.Session{
		ID: "s2", WorkspaceID: ws.ID, Branch: "feat/s2",
		Status: registry.StatusRunning, TmuxName: "t2",
	})

	m := New(t.TempDir(), reg, ws.ID, config.Default())
	be := &captureBackend{
		panes: map[string]string{},
		alive: map[string]bool{"t1": true, "t2": true},
	}
	clk := newFakeClock()
	m.tmux = be
	m.stream = nil
	m.now = clk.now
	m.statuses = newStatusTracker(clk.now)

	var recs []notifRec
	rp := &recs
	m.notify = func(title, body string) {
		*rp = append(*rp, notifRec{title, body})
	}
	return m, be, clk, rp
}

func TestNotifyOnWaitingTransitionSuppressesFocused(t *testing.T) {
	m, be, clk, recs := notifyModel(t)
	be.panes["t1"] = "$ " // focused session also reaches a prompt
	be.panes["t2"] = "$ "

	m.sweepStatuses() // first observation: both working, no transition
	if len(*recs) != 0 {
		t.Fatalf("notified on first observation: %v", *recs)
	}

	clk.advance(workingWindow + time.Second)
	m.sweepStatuses() // both settle to waiting

	if len(*recs) != 1 {
		t.Fatalf("want exactly one notification, got %d: %v", len(*recs), *recs)
	}
	if !strings.Contains((*recs)[0].body, "feat/s2") {
		t.Fatalf("notification not for s2: %q", (*recs)[0].body)
	}
	if strings.Contains((*recs)[0].body, "feat/s1") {
		t.Fatal("notified for the focused session")
	}
}

func TestNotifyOnExitedTransition(t *testing.T) {
	m, be, clk, recs := notifyModel(t)
	be.panes["t2"] = "running output"

	m.sweepStatuses() // s2 working
	clk.advance(time.Second)

	be.alive["t2"] = false // tmux session vanishes
	m.sweepStatuses()      // reconcile marks s2 exited

	if len(*recs) != 1 {
		t.Fatalf("want one exit notification, got %d: %v", len(*recs), *recs)
	}
	if !strings.Contains((*recs)[0].body, "exited") ||
		!strings.Contains((*recs)[0].body, "feat/s2") {
		t.Fatalf("unexpected exit notification: %q", (*recs)[0].body)
	}
	if s2, _ := m.reg.Session("s2"); s2.Status != registry.StatusExited {
		t.Fatal("reconcile did not persist s2 as exited")
	}
}

func TestNotifyOffFiresNothing(t *testing.T) {
	m, be, clk, recs := notifyModel(t)
	m.cfg.Notify = config.NotifyOff
	be.panes["t2"] = "$ "

	m.sweepStatuses()
	clk.advance(workingWindow + time.Second)
	m.sweepStatuses()
	be.alive["t2"] = false
	m.sweepStatuses()

	if len(*recs) != 0 {
		t.Fatalf("off mode fired notifications: %v", *recs)
	}
}

func TestNotifyDebouncesFlapping(t *testing.T) {
	m, be, clk, recs := notifyModel(t)
	be.panes["t2"] = "$ "

	m.sweepStatuses() // working
	clk.advance(workingWindow + time.Second)
	m.sweepStatuses() // waiting -> notify (1)

	// Flap: produce output (working), then fall quiet at a prompt again
	// within the debounce window.
	clk.advance(time.Second)
	be.panes["t2"] = "busy output"
	m.sweepStatuses() // working, not notifiable
	clk.advance(workingWindow + time.Second)
	be.panes["t2"] = "$ "
	m.sweepStatuses() // waiting again, but inside the debounce window

	if len(*recs) != 1 {
		t.Fatalf("debounce failed: want 1 notification, got %d: %v",
			len(*recs), *recs)
	}
}
