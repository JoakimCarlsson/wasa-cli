package pane

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/joakimcarlsson/wasa/internal/backend"
	"github.com/joakimcarlsson/wasa/internal/config"
	"github.com/joakimcarlsson/wasa/internal/tui/theme"
	"github.com/joakimcarlsson/wasa/internal/worktree"
)

func testTheme() theme.Theme {
	return theme.NewTheme(config.Default().Theme)
}

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

func newPreview(t *testing.T) (*Preview, *fakeStream) {
	t.Helper()
	fs := &fakeStream{}
	p := NewPreview(fs, fs)
	return &p, fs
}

func TestPreviewSetTargetOpensStream(t *testing.T) {
	p, fs := newPreview(t)

	cmd := p.SetTarget("wasa_s1")
	if _, live := p.Capture(); !live {
		t.Fatal("no watcher opened for the running target")
	}
	if cmd == nil {
		t.Fatal("SetTarget returned no wait command for the new stream")
	}
	if len(fs.watched) != 1 || fs.watched[0] != "wasa_s1" {
		t.Fatalf("watched = %v, want [wasa_s1]", fs.watched)
	}
	if p.WatchedName() != "wasa_s1" {
		t.Fatalf("WatchedName = %q, want wasa_s1", p.WatchedName())
	}
}

func TestApplyPreviewStoresContentAndReArms(t *testing.T) {
	p, _ := newPreview(t)
	p.SetTarget("wasa_s1")

	cmd := p.Apply(PreviewMsg{gen: p.watchGen, content: "live", ok: true})
	if got, _ := p.Capture(); got != "live" {
		t.Fatalf("preview = %q, want %q", got, "live")
	}
	if cmd == nil {
		t.Fatal("Apply did not re-arm the wait on the live stream")
	}
}

func TestApplyPreviewIgnoresStaleGeneration(t *testing.T) {
	p, _ := newPreview(t)
	p.SetTarget("wasa_s1")
	p.preview = "current"

	cmd := p.Apply(PreviewMsg{gen: p.watchGen + 1, content: "stale", ok: true})
	if got, _ := p.Capture(); got != "current" {
		t.Fatalf("stale delivery overwrote preview: %q", got)
	}
	if cmd != nil {
		t.Fatal("stale delivery re-armed a wait")
	}
}

func TestSwitchTargetMovesStream(t *testing.T) {
	p, fs := newPreview(t)
	p.SetTarget("wasa_s1")
	old := p.watcher.(*fakeWatcher)

	cmd := p.SetTarget("wasa_s2")
	if !old.isClosed() {
		t.Fatal("old stream was not closed when target moved")
	}
	if p.watcher == nil || p.watcher == any(old) {
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
	p, _ := newPreview(t)
	p.SetTarget("wasa_s1")
	gen := p.watchGen

	cmd := p.Apply(PreviewMsg{gen: gen, ok: false})
	if _, live := p.Capture(); live {
		t.Fatal("dropped stream was not torn down")
	}
	if cmd != nil {
		t.Fatal("dropped stream re-armed a wait instead of degrading")
	}
	if rc := p.PollOrReconnect("wasa_s1"); rc == nil || p.watcher == nil {
		t.Fatal("fallback tick did not reconnect the dropped stream")
	}
}

func TestCloseClosesWatcher(t *testing.T) {
	p, _ := newPreview(t)
	p.SetTarget("wasa_s1")
	w := p.watcher.(*fakeWatcher)

	p.Close()
	if !w.isClosed() {
		t.Fatal("Close left the control client open (orphaned)")
	}
}

func TestWatchFailureFallsBackToPoll(t *testing.T) {
	fs := &fakeStream{failOn: "wasa_s1"}
	p := NewPreview(fs, fs)

	if cmd := p.SetTarget("wasa_s1"); cmd != nil {
		t.Fatal("SetTarget returned a wait command despite a failed Watch")
	}
	if p.watcher != nil {
		t.Fatal("a watcher was retained after Watch failed")
	}
	if rc := p.PollOrReconnect("wasa_s1"); rc != nil {
		t.Fatal("PollOrReconnect returned a stream command after a failed Watch")
	}
	if fs.captures == 0 {
		t.Fatal("fallback poll did not call Capture")
	}
}

func TestEmptyTargetOpensNoStream(t *testing.T) {
	p, fs := newPreview(t)

	if cmd := p.SetTarget(""); cmd != nil {
		t.Fatal("SetTarget streamed an empty target")
	}
	if p.watcher != nil || len(fs.watched) != 0 {
		t.Fatalf("opened a stream for an empty target: %v", fs.watched)
	}
}

func TestNoStreamBackendUsesPoll(t *testing.T) {
	fs := &fakeStream{}
	p := NewPreview(nil, fs)

	if cmd := p.SetTarget("wasa_s1"); cmd != nil {
		t.Fatal("SetTarget streamed without a streaming backend")
	}
	p.PollOrReconnect("wasa_s1")
	if fs.captures == 0 {
		t.Fatal("non-streaming backend did not fall back to Capture poll")
	}
}

func TestTerminalEnsureSpawnsAndCaptures(t *testing.T) {
	term := NewTerminal()
	fb := newFakeBackend()
	fb.captures["wasa_x_s1_term"] = "user@host:~$ "

	msg, ok := term.EnsureCmd("wasa_x_s1", "/dir", fb)().(TermMsg)
	if !ok {
		t.Fatal("EnsureCmd did not return a TermMsg")
	}
	if msg.err != nil {
		t.Fatalf("ensure errored: %v", msg.err)
	}
	if len(fb.spawned) != 1 || fb.spawned[0] != "wasa_x_s1_term" {
		t.Fatalf("companion not spawned with _term suffix: %v", fb.spawned)
	}

	term.Apply(msg, "wasa_x_s1_term")
	if !term.Tracking("wasa_x_s1_term") {
		t.Fatal("companion not recorded for teardown")
	}
	if term.content != "user@host:~$ " {
		t.Fatalf("capture not stored: %q", term.content)
	}
}

func TestTerminalReusesExistingCompanion(t *testing.T) {
	term := NewTerminal()
	fb := newFakeBackend()
	fb.sessions["wasa_x_s1_term"] = true

	term.EnsureCmd("wasa_x_s1", "/dir", fb)()
	if len(fb.spawned) != 0 {
		t.Fatalf("existing companion was respawned: %v", fb.spawned)
	}
}

// TestTerminalDropsStaleCapture guards that a capture delivered for a companion
// that is not the expected one does not overwrite the body.
func TestTerminalDropsStaleCapture(t *testing.T) {
	term := NewTerminal()

	term.Apply(TermMsg{name: "someone_else_term", content: "stale"}, "wasa_x_s1_term")
	if term.content != "" {
		t.Fatalf("stale capture overwrote the body: %q", term.content)
	}
	if !term.Tracking("someone_else_term") {
		t.Fatal("companion should still be tracked for teardown")
	}
}

func TestTerminalAttachSpawnsAndTargetsCompanion(t *testing.T) {
	term := NewTerminal()
	fb := newFakeBackend()

	cmd, err := term.AttachCmd("wasa_x_s1", "/dir", fb)
	if err != nil {
		t.Fatalf("AttachCmd errored: %v", err)
	}
	if cmd == nil {
		t.Fatal("terminal attach produced no exec command")
	}
	if len(fb.spawned) != 1 || fb.spawned[0] != "wasa_x_s1_term" {
		t.Fatalf("attach did not spawn the companion: %v", fb.spawned)
	}
	if len(fb.attached) != 1 || fb.attached[0] != "wasa_x_s1_term" {
		t.Fatalf("attach targeted the wrong session: %v", fb.attached)
	}
	if !term.Tracking("wasa_x_s1_term") {
		t.Fatal("attach did not record the companion for teardown")
	}
}

func TestCloseKillsAllCompanions(t *testing.T) {
	term := NewTerminal()
	fb := newFakeBackend()
	fb.sessions["a_term"] = true
	fb.sessions["b_term"] = true
	term.terms = map[string]bool{"a_term": true, "b_term": true}

	term.Close(fb)
	if len(fb.killed) != 2 {
		t.Fatalf("Close killed %d companions, want 2", len(fb.killed))
	}
	if len(term.terms) != 0 {
		t.Fatalf("terms not cleared after close: %v", term.terms)
	}
}

func TestTerminalEnsureNoSelectionClearsBody(t *testing.T) {
	term := NewTerminal()
	fb := newFakeBackend()
	term.shown = "stale_term"
	term.content = "stale"

	term.Apply(term.EnsureCmd("", "", fb)().(TermMsg), "")
	if term.content != "" || term.shown != "" {
		t.Fatalf("no selection should clear the body: shown=%q content=%q",
			term.shown, term.content)
	}
}

func TestDiffBodyPlainSessionExplains(t *testing.T) {
	d := NewDiff(testTheme())
	if cmd := d.EnsureCmd("p1", "/repo", t.TempDir(), "ws", "", ""); cmd == nil {
		t.Fatal("plain session should still produce a load command")
	}
	sess := DiffSession{Selected: true, ID: "p1"}
	body := d.Body(testTheme(), sess, 100, 20)
	if !strings.Contains(body, "only available for worktree sessions") {
		t.Fatalf("plain session diff body = %q", body)
	}
}

func TestDiffBodyLoadingBeforeCompute(t *testing.T) {
	d := NewDiff(testTheme())
	sess := DiffSession{
		Selected: true, ID: "w1", Branch: "feature/w1",
		WorktreePath: "/wt/w1", BaseCommit: "deadbeef",
	}
	if body := d.Body(testTheme(), sess, 100, 20); !strings.Contains(body, "Loading diff") {
		t.Fatalf("uncomputed worktree diff body = %q", body)
	}
}

func TestEnsureDiffCmdGuardsAlreadyLoaded(t *testing.T) {
	d := NewDiff(testTheme())
	d.sid = "w1"
	if cmd := d.EnsureCmd(
		"w1", "/repo", t.TempDir(), "ws", "/wt/w1", "deadbeef",
	); cmd != nil {
		t.Fatal("EnsureCmd recomputed an already-loaded diff")
	}
}

// TestColorizeDiffPreservesContent checks the line-by-line pass keeps every
// line and its order intact. It cannot assert ANSI: lipgloss renders plain
// under the test runner's no-color profile, so the colours are verified
// manually.
func TestColorizeDiffPreservesContent(t *testing.T) {
	in := "@@ -1 +1 @@\n+added\n-removed\n context"
	out := colorizeDiff(testTheme(), in)
	for _, want := range []string{"@@ -1 +1 @@", "added", "removed", " context"} {
		if !strings.Contains(out, want) {
			t.Fatalf("colorizeDiff dropped %q from:\n%s", want, out)
		}
	}
	if got := strings.Count(out, "\n"); got != 3 {
		t.Fatalf("colorizeDiff changed line count: %d newlines, want 3", got)
	}
}

// TestDiffTabShowsWorktreeChanges drives the full diff pipeline against a real
// git repository: it records a base commit, makes a change in the worktree,
// then computes and renders the diff exactly as the Diff tab does.
func TestDiffTabShowsWorktreeChanges(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	home := t.TempDir()
	repo := t.TempDir()
	gitInit(t, repo)

	wm := worktree.New(repo, home, "ws")
	base, err := wm.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	wt, err := wm.Add("feature/x")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(wt, "f.txt"), []byte("one\ntwo\n"), 0o644,
	); err != nil {
		t.Fatal(err)
	}

	d := NewDiff(testTheme())
	cmd := d.EnsureCmd("d1", repo, home, "ws", wt, base)
	if cmd == nil {
		t.Fatal("expected a diff command for a worktree session")
	}
	msg, ok := cmd().(DiffMsg)
	if !ok || msg.err != nil {
		t.Fatalf("diff command failed: %+v", msg)
	}
	d.Size(100, 20)
	d.Apply(msg)

	if d.added != 2 {
		t.Fatalf("diff added = %d, want 2", d.added)
	}
	sess := DiffSession{
		Selected: true, ID: "d1", Branch: "feature/x",
		WorktreePath: wt, BaseCommit: base,
	}
	body := d.Body(testTheme(), sess, 100, 20)
	if !strings.Contains(body, "2 additions(+)") {
		t.Fatalf("diff body missing summary line:\n%s", body)
	}
	if !strings.Contains(body, "f.txt") {
		t.Fatalf("diff body missing the changed file:\n%s", body)
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	run("commit", "--allow-empty", "-m", "initial")
}
