package tui

import (
	"os/exec"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/joakimcarlsson/wasa/internal/config"
	"github.com/joakimcarlsson/wasa/internal/registry"
	"github.com/joakimcarlsson/wasa/internal/tui/pane"
)

// attachBackend is an in-memory SessionBackend that records spawns and attaches
// so the root attach dispatch (agent vs companion) can be exercised without a
// tmux server.
type attachBackend struct {
	sessions map[string]bool
	spawned  []string
	attached []string
}

func newAttachBackend() *attachBackend {
	return &attachBackend{sessions: map[string]bool{}}
}

func (b *attachBackend) SpawnEnv(name, _ string, _ []string, _ ...string) error {
	b.sessions[name] = true
	b.spawned = append(b.spawned, name)
	return nil
}

func (b *attachBackend) AttachCmd(name string) (*exec.Cmd, error) {
	b.attached = append(b.attached, name)
	return exec.Command("true"), nil
}

func (b *attachBackend) Capture(string) (string, error) { return "", nil }
func (b *attachBackend) Has(name string) (bool, error)  { return b.sessions[name], nil }
func (b *attachBackend) List() ([]string, error)        { return nil, nil }
func (b *attachBackend) Kill(string) error              { return nil }

// paneModel builds a one-workspace cockpit with a single running session
// selected, sized so the full (non-compact) frame renders.
func paneModel(t *testing.T) Model {
	t.Helper()
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	reg.AddSession(&registry.Session{
		ID: "s1", WorkspaceID: ws.ID, Branch: "feat/s1",
		TmuxName: "wasa_x_s1", Status: registry.StatusRunning,
	})
	m := New(t.TempDir(), reg, ws.ID, config.Default())
	m.width, m.height = 120, 30
	return m
}

func TestCyclePaneTabWraps(t *testing.T) {
	m := paneModel(t)
	if m.pane != panePreview {
		t.Fatalf("initial pane = %d, want panePreview", m.pane)
	}
	m.cyclePaneTab(1)
	if m.pane != paneDiff {
		t.Fatalf("after one cycle pane = %d, want paneDiff", m.pane)
	}
	m.cyclePaneTab(1)
	if m.pane != paneTerminal {
		t.Fatalf("after two cycles pane = %d, want paneTerminal", m.pane)
	}
	m.cyclePaneTab(1)
	if m.pane != panePreview {
		t.Fatalf("cycle past end pane = %d, want wrap to panePreview", m.pane)
	}
}

// TestPaneTabGatesPreviewTarget is the guard that only the active tab does
// per-tick work: with a running session selected the Preview tab targets its
// tmux stream, but cycling to Diff or Terminal must yield no target, so the
// watcher tears down and neither the stream nor the capture poll runs.
func TestPaneTabGatesPreviewTarget(t *testing.T) {
	m := paneModel(t)
	if got := m.previewTarget(); got != "wasa_x_s1" {
		t.Fatalf("Preview tab target = %q, want the session's tmux name", got)
	}
	m.cyclePaneTab(1) // Diff
	if got := m.previewTarget(); got != "" {
		t.Fatalf("Diff tab target = %q, want empty", got)
	}
	m.cyclePaneTab(1) // Terminal
	if got := m.previewTarget(); got != "" {
		t.Fatalf("Terminal tab target = %q, want empty", got)
	}
	m.cyclePaneTab(1) // back to Preview
	if got := m.previewTarget(); got != "wasa_x_s1" {
		t.Fatalf("returning to Preview target = %q, want resumed", got)
	}
}

func TestPaneTabKeyCyclesPane(t *testing.T) {
	m := paneModel(t)
	ctrlT := m.keys.Primary(config.ActionPaneTab)
	if ctrlT == "" {
		t.Fatal("pane-tab action is unbound")
	}

	next, _ := m.updateList(tea.KeyMsg{Type: tea.KeyCtrlT})
	got := next.(Model)
	if got.pane != paneDiff {
		t.Fatalf("pane-tab key did not advance the pane: %d", got.pane)
	}
}

func TestPaneTabStripRendersAllTabs(t *testing.T) {
	m := paneModel(t)
	view := m.View()
	for _, label := range paneTabNames {
		if !strings.Contains(view, label) {
			t.Fatalf("view missing pane tab %q:\n%s", label, view)
		}
	}
}

// TestTerminalAttachTargetsCompanion is the root dispatch test: on the Terminal
// tab attach spawns and attaches the selected session's companion shell and
// records it for teardown.
func TestTerminalAttachTargetsCompanion(t *testing.T) {
	m := paneModel(t)
	be := newAttachBackend()
	m.tmux = be
	m.term = pane.NewTerminal()
	m.pane = paneTerminal

	next, cmd := m.attach()
	got := next.(Model)
	if cmd == nil {
		t.Fatal("terminal attach produced no exec command")
	}
	if len(be.spawned) != 1 || be.spawned[0] != "wasa_x_s1_term" {
		t.Fatalf("attach did not spawn the companion: %v", be.spawned)
	}
	if len(be.attached) != 1 || be.attached[0] != "wasa_x_s1_term" {
		t.Fatalf("attach targeted the wrong session: %v", be.attached)
	}
	if !got.term.Tracking("wasa_x_s1_term") {
		t.Fatal("attach did not record the companion for teardown")
	}
}

// TestPreviewAttachTargetsAgentSession is the root dispatch test for the
// Preview tab: attach targets the agent session, not a companion.
func TestPreviewAttachTargetsAgentSession(t *testing.T) {
	m := paneModel(t)
	be := newAttachBackend()
	m.tmux = be

	m.attach()
	if len(be.attached) != 1 || be.attached[0] != "wasa_x_s1" {
		t.Fatalf(
			"preview attach should target the agent session: %v",
			be.attached,
		)
	}
}

// TestApplyDiffDropsStaleDelivery is the root stale-drop guard: a diff for a
// session that is no longer selected must not be applied.
func TestApplyDiffDropsStaleDelivery(t *testing.T) {
	m := paneModel(t)
	m.applyDiff(pane.NewDiffErr("not-the-selected-one", nil))
	if m.diff.SID() != "" {
		t.Fatalf("stale diff was applied: sid=%q", m.diff.SID())
	}
}
