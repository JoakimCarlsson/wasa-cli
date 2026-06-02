package tui

import (
	"os/exec"
	"testing"

	"github.com/joakimcarlsson/wasa/internal/config"
	"github.com/joakimcarlsson/wasa/internal/registry"
)

// fakeBackend is an in-memory backend.SessionBackend for exercising the
// companion-shell lifecycle without a tmux server. It records spawns, attaches
// and kills, and answers Has/Capture from its session map.
type fakeBackend struct {
	sessions map[string]bool
	captures map[string]string
	spawned  []string
	attached []string
	killed   []string
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		sessions: map[string]bool{},
		captures: map[string]string{},
	}
}

func (f *fakeBackend) SpawnEnv(
	name, _ string, _ []string, _ ...string,
) error {
	f.sessions[name] = true
	f.spawned = append(f.spawned, name)
	return nil
}

func (f *fakeBackend) AttachCmd(name string) (*exec.Cmd, error) {
	f.attached = append(f.attached, name)
	return exec.Command("true"), nil
}

func (f *fakeBackend) Capture(name string) (string, error) {
	return f.captures[name], nil
}

func (f *fakeBackend) Has(name string) (bool, error) {
	return f.sessions[name], nil
}

func (f *fakeBackend) List() ([]string, error) {
	out := make([]string, 0, len(f.sessions))
	for n := range f.sessions {
		out = append(out, n)
	}
	return out, nil
}

func (f *fakeBackend) Kill(name string) error {
	delete(f.sessions, name)
	f.killed = append(f.killed, name)
	return nil
}

func TestTerminalEnsureSpawnsAndCaptures(t *testing.T) {
	m := paneModel(t)
	fb := newFakeBackend()
	fb.captures["wasa_x_s1_term"] = "user@host:~$ "
	m.tmux = fb
	m.tabs.terminal.tmux = fb
	m.tabs.active = paneTerminal

	msg, ok := m.tabs.terminal.ensure(m.selectedSession())().(termMsg)
	if !ok {
		t.Fatal("ensure did not return a termMsg")
	}
	if msg.err != nil {
		t.Fatalf("ensure errored: %v", msg.err)
	}
	if len(fb.spawned) != 1 || fb.spawned[0] != "wasa_x_s1_term" {
		t.Fatalf("companion not spawned with _term suffix: %v", fb.spawned)
	}

	_ = m.tabs.terminal.apply(msg, m.selectedSession())
	if !m.tabs.terminal.spawned["wasa_x_s1_term"] {
		t.Fatal("companion not recorded for teardown")
	}
	if m.tabs.terminal.content != "user@host:~$ " {
		t.Fatalf("capture not stored: %q", m.tabs.terminal.content)
	}
}

func TestTerminalReusesExistingCompanion(t *testing.T) {
	m := paneModel(t)
	fb := newFakeBackend()
	fb.sessions["wasa_x_s1_term"] = true
	m.tmux = fb
	m.tabs.terminal.tmux = fb
	m.tabs.active = paneTerminal

	m.tabs.terminal.ensure(m.selectedSession())()
	if len(fb.spawned) != 0 {
		t.Fatalf("existing companion was respawned: %v", fb.spawned)
	}
}

// TestTerminalDropsStaleCapture guards that a capture delivered for a companion
// that is no longer the selected session's does not overwrite the body.
func TestTerminalDropsStaleCapture(t *testing.T) {
	m := paneModel(t)
	m.tabs.terminal.tmux = newFakeBackend()

	_ = m.tabs.terminal.apply(
		termMsg{name: "someone_else_term", content: "stale"}, m.selectedSession(),
	)
	if m.tabs.terminal.content != "" {
		t.Fatalf("stale capture overwrote the body: %q", m.tabs.terminal.content)
	}
	if !m.tabs.terminal.spawned["someone_else_term"] {
		t.Fatal("companion should still be tracked for teardown")
	}
}

func TestTerminalAttachSpawnsAndTargetsCompanion(t *testing.T) {
	m := paneModel(t)
	fb := newFakeBackend()
	m.tmux = fb
	m.tabs.terminal.tmux = fb
	m.tabs.active = paneTerminal

	next, cmd := m.attach()
	got := next.(Model)
	if cmd == nil {
		t.Fatal("terminal attach produced no exec command")
	}
	if len(fb.spawned) != 1 || fb.spawned[0] != "wasa_x_s1_term" {
		t.Fatalf("attach did not spawn the companion: %v", fb.spawned)
	}
	if len(fb.attached) != 1 || fb.attached[0] != "wasa_x_s1_term" {
		t.Fatalf("attach targeted the wrong session: %v", fb.attached)
	}
	if !got.tabs.terminal.spawned["wasa_x_s1_term"] {
		t.Fatal("attach did not record the companion for teardown")
	}
}

func TestPreviewAttachTargetsAgentSession(t *testing.T) {
	m := paneModel(t)
	fb := newFakeBackend()
	m.tmux = fb

	m.attach()
	if len(fb.attached) != 1 || fb.attached[0] != "wasa_x_s1" {
		t.Fatalf(
			"preview attach should target the agent session: %v",
			fb.attached,
		)
	}
}

func TestCloseTermsKillsAllCompanions(t *testing.T) {
	m := paneModel(t)
	fb := newFakeBackend()
	fb.sessions["a_term"] = true
	fb.sessions["b_term"] = true
	m.tabs.terminal.tmux = fb
	m.tabs.terminal.spawned = map[string]bool{"a_term": true, "b_term": true}

	m.tabs.terminal.close()
	if len(fb.killed) != 2 {
		t.Fatalf("close killed %d companions, want 2", len(fb.killed))
	}
	if len(m.tabs.terminal.spawned) != 0 {
		t.Fatalf("spawned not cleared after close: %v", m.tabs.terminal.spawned)
	}
}

func TestTerminalEnsureNoSelectionClearsBody(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	m := New(t.TempDir(), reg, ws.ID, config.Default())
	m.tabs.terminal.tmux = newFakeBackend()
	m.tabs.active = paneTerminal
	m.tabs.terminal.shown = "stale_term"
	m.tabs.terminal.content = "stale"

	_ = m.tabs.terminal.apply(
		m.tabs.terminal.ensure(m.selectedSession())().(termMsg), m.selectedSession(),
	)
	if m.tabs.terminal.content != "" || m.tabs.terminal.shown != "" {
		t.Fatalf("no selection should clear the body: shown=%q content=%q",
			m.tabs.terminal.shown, m.tabs.terminal.content)
	}
}
