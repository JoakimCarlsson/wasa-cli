package tui

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/joakimcarlsson/wasa/internal/config"
	"github.com/joakimcarlsson/wasa/internal/registry"
	"github.com/joakimcarlsson/wasa/internal/sessionstatus"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)}
}

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
	m.tabs.preview.tmux = be
	m.tabs.preview.stream = nil
	m.now = clk.now
	m.statuses = sessionstatus.NewTracker(clk.now)

	var recs []notifRec
	rp := &recs
	m.notify = func(title, body string) {
		*rp = append(*rp, notifRec{title, body})
	}
	return m, be, clk, rp
}

func TestNotifyOnWaitingTransitionSuppressesFocused(t *testing.T) {
	m, be, clk, recs := notifyModel(t)
	be.panes["t1"] = "$ "
	be.panes["t2"] = "$ "

	m.sweepStatuses()
	if len(*recs) != 0 {
		t.Fatalf("notified on first observation: %v", *recs)
	}

	clk.advance(sessionstatus.WorkingWindow + time.Second)
	m.sweepStatuses()

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

	m.sweepStatuses()
	clk.advance(time.Second)

	be.alive["t2"] = false
	m.sweepStatuses()

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
	clk.advance(sessionstatus.WorkingWindow + time.Second)
	m.sweepStatuses()
	be.alive["t2"] = false
	m.sweepStatuses()

	if len(*recs) != 0 {
		t.Fatalf("off mode fired notifications: %v", *recs)
	}
}

func TestFreshHookOverridesScrape(t *testing.T) {
	m, be, clk, recs := notifyModel(t)
	s2, _ := m.reg.Session("s2")
	m.lastStatus["s2"] = sessionstatus.Working
	be.panes["t2"] = "compiling project"

	if err := sessionstatus.Write(m.home, "s2", sessionstatus.Record{
		Status:    sessionstatus.Waiting,
		Event:     "Notification",
		UpdatedAt: clk.now(),
	}); err != nil {
		t.Fatal(err)
	}

	m.sweepStatuses()

	if got := m.runtimeStatus(s2); got != sessionstatus.Waiting {
		t.Fatalf("hook did not override scrape: status = %q, want waiting", got)
	}
	if len(*recs) != 1 || !strings.Contains((*recs)[0].body, "feat/s2") {
		t.Fatalf("fresh waiting hook did not notify: %v", *recs)
	}
}

func TestNotifyDebouncesFlapping(t *testing.T) {
	m, _, _, recs := notifyModel(t)
	s2, _ := m.reg.Session("s2")
	m.lastStatus["s2"] = sessionstatus.Working

	m.transition(s2, sessionstatus.Waiting, "")
	m.transition(s2, sessionstatus.Working, "")
	m.transition(s2, sessionstatus.Waiting, "")
	m.transition(s2, sessionstatus.Working, "")
	m.transition(s2, sessionstatus.Waiting, "")

	if len(*recs) != 1 {
		t.Fatalf("debounce failed: want 1 notification, got %d: %v",
			len(*recs), *recs)
	}
}
