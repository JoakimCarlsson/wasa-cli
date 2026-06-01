package tui

import (
	"errors"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/joakimcarlsson/wasa/internal/backend"
	"github.com/joakimcarlsson/wasa/internal/config"
	"github.com/joakimcarlsson/wasa/internal/registry"
)

// fakeWatcher is a backend.Watcher backed by an in-memory channel, recording
// whether it was closed so tests can assert lifecycle teardown.
type fakeWatcher struct {
	updates chan string
	mu      sync.Mutex
	closed  bool
}

func (w *fakeWatcher) Updates() <-chan string { return w.updates }

func (w *fakeWatcher) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed {
		w.closed = true
		close(w.updates)
	}
	return nil
}

func (w *fakeWatcher) isClosed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closed
}

// fakeStream is a SessionBackend that also streams. It records every Watch and
// can be made to fail for a given name to exercise graceful degradation.
type fakeStream struct {
	backend.SessionBackend
	watched  []string
	watchers []*fakeWatcher
	failOn   string
	captures int
}

func (s *fakeStream) Watch(name string) (backend.Watcher, error) {
	if name == s.failOn {
		return nil, errors.New("watch failed")
	}
	w := &fakeWatcher{updates: make(chan string, 1)}
	s.watched = append(s.watched, name)
	s.watchers = append(s.watchers, w)
	return w, nil
}

// Capture stands in for the fallback poll so non-streaming paths don't panic.
func (s *fakeStream) Capture(string) (string, error) {
	s.captures++
	return "polled", nil
}

// streamModel builds a model over a workspace with two running sessions and a
// fake streaming backend wired in.
func streamModel(t *testing.T) (Model, *fakeStream) {
	t.Helper()
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	reg.AddSession(&registry.Session{
		ID: "s1", WorkspaceID: ws.ID, Branch: "feat/s1",
		Status: registry.StatusRunning, TmuxName: "wasa_s1",
	})
	reg.AddSession(&registry.Session{
		ID: "s2", WorkspaceID: ws.ID, Branch: "feat/s2",
		Status: registry.StatusRunning, TmuxName: "wasa_s2",
	})

	m := New(t.TempDir(), reg, ws.ID, config.Default())
	fs := &fakeStream{}
	m.tmux = fs
	m.tabs.preview.tmux = fs
	m.tabs.preview.stream = fs
	m.tabs.terminal.tmux = fs
	return m, fs
}

// target is the preview target for the current selection, the input the model
// feeds the preview pane on every retarget.
func (m *Model) target() string {
	return m.tabs.previewTarget(m.selectedSession())
}

func TestEnsureWatcherOpensStreamForSelected(t *testing.T) {
	m, fs := streamModel(t)

	cmd := m.tabs.retargetPreview(m.selectedSession())
	if m.tabs.preview.watcher == nil {
		t.Fatal("no watcher opened for the running selected session")
	}
	if cmd == nil {
		t.Fatal("retarget returned no wait command for the new stream")
	}
	if len(fs.watched) != 1 || fs.watched[0] != "wasa_s1" {
		t.Fatalf("watched = %v, want [wasa_s1]", fs.watched)
	}
}

func TestApplyPreviewStoresContentAndReArms(t *testing.T) {
	m, _ := streamModel(t)
	m.tabs.retargetPreview(m.selectedSession())

	cmd := m.tabs.preview.apply(
		previewMsg{gen: m.tabs.preview.watchGen, content: "live", ok: true},
	)
	if m.tabs.preview.content != "live" {
		t.Fatalf("preview = %q, want %q", m.tabs.preview.content, "live")
	}
	if cmd == nil {
		t.Fatal("apply did not re-arm the wait on the live stream")
	}
}

func TestApplyPreviewIgnoresStaleGeneration(t *testing.T) {
	m, _ := streamModel(t)
	m.tabs.retargetPreview(m.selectedSession())
	m.tabs.preview.content = "current"

	cmd := m.tabs.preview.apply(
		previewMsg{gen: m.tabs.preview.watchGen + 1, content: "stale", ok: true},
	)
	if m.tabs.preview.content != "current" {
		t.Fatalf("stale delivery overwrote preview: %q", m.tabs.preview.content)
	}
	if cmd != nil {
		t.Fatal("stale delivery re-armed a wait")
	}
}

func TestSwitchSelectionMovesStream(t *testing.T) {
	m, fs := streamModel(t)
	m.tabs.retargetPreview(m.selectedSession())
	old := m.tabs.preview.watcher.(*fakeWatcher)

	m.cursor = 1
	cmd := m.tabs.retargetPreview(m.selectedSession())
	if !old.isClosed() {
		t.Fatal("old stream was not closed when selection moved")
	}
	if m.tabs.preview.watcher == nil || m.tabs.preview.watcher == any(old) {
		t.Fatal("stream was not re-targeted to the new selection")
	}
	if cmd == nil {
		t.Fatal("no wait command for the re-targeted stream")
	}
	if len(fs.watched) != 2 || fs.watched[1] != "wasa_s2" {
		t.Fatalf("watched = %v, want second watch on wasa_s2", fs.watched)
	}
}

func TestDroppedStreamDegradesGracefully(t *testing.T) {
	m, _ := streamModel(t)
	m.tabs.retargetPreview(m.selectedSession())
	gen := m.tabs.preview.watchGen

	cmd := m.tabs.preview.apply(previewMsg{gen: gen, ok: false})
	if m.tabs.preview.watcher != nil {
		t.Fatal("dropped stream was not torn down")
	}
	if cmd != nil {
		t.Fatal("dropped stream re-armed a wait instead of degrading")
	}
	if rc := m.tabs.preview.pollOrReconnect(m.target()); rc == nil ||
		m.tabs.preview.watcher == nil {
		t.Fatal("fallback tick did not reconnect the dropped stream")
	}
}

func TestQuitClosesWatcher(t *testing.T) {
	m, _ := streamModel(t)
	m.tabs.retargetPreview(m.selectedSession())
	w := m.tabs.preview.watcher.(*fakeWatcher)

	if _, cmd := m.updateList(
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")},
	); cmd == nil {
		t.Fatal("q did not produce the quit command")
	}
	if !w.isClosed() {
		t.Fatal("quit left the control client open (orphaned)")
	}
}

func TestWatchFailureFallsBackToPoll(t *testing.T) {
	m, fs := streamModel(t)
	fs.failOn = "wasa_s1"

	if cmd := m.tabs.retargetPreview(m.selectedSession()); cmd != nil {
		t.Fatal("retarget returned a wait command despite a failed Watch")
	}
	if m.tabs.preview.watcher != nil {
		t.Fatal("a watcher was retained after Watch failed")
	}
	if rc := m.tabs.preview.pollOrReconnect(m.target()); rc != nil {
		t.Fatal(
			"pollOrReconnect returned a stream command after a failed Watch",
		)
	}
	if fs.captures == 0 {
		t.Fatal("fallback poll did not call Capture")
	}
}

func TestExitedSelectionOpensNoStream(t *testing.T) {
	m, fs := streamModel(t)
	s1, _ := m.reg.Session("s1")
	s1.Status = registry.StatusExited
	s2, _ := m.reg.Session("s2")
	s2.Status = registry.StatusExited

	if cmd := m.tabs.retargetPreview(m.selectedSession()); cmd != nil {
		t.Fatal("retarget streamed an exited session")
	}
	if m.tabs.preview.watcher != nil || len(fs.watched) != 0 {
		t.Fatalf("opened a stream for an exited session: %v", fs.watched)
	}
}

func TestNoStreamBackendUsesPoll(t *testing.T) {
	m, fs := streamModel(t)
	m.tabs.preview.stream = nil

	if cmd := m.tabs.retargetPreview(m.selectedSession()); cmd != nil {
		t.Fatal("retarget streamed without a streaming backend")
	}
	m.tabs.preview.pollOrReconnect(m.target())
	if fs.captures == 0 {
		t.Fatal("non-streaming backend did not fall back to Capture poll")
	}
}
