package tui

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/joakimcarlsson/wasa/internal/config"
	"github.com/joakimcarlsson/wasa/internal/registry"
	"github.com/joakimcarlsson/wasa/internal/repo"
	"github.com/joakimcarlsson/wasa/internal/tui/pane"
)

// previewColorBackend is a non-streaming SessionBackend whose Capture returns a
// fixed pane content, so the preview render path can be exercised end to end.
type previewColorBackend struct{ content string }

func (b *previewColorBackend) SpawnEnv(
	string,
	string,
	[]string,
	...string,
) error {
	return nil
}

func (b *previewColorBackend) AttachCmd(
	string,
) (*exec.Cmd, error) {
	return nil, nil
}

func (b *previewColorBackend) Capture(
	string,
) (string, error) {
	return b.content, nil
}

func (b *previewColorBackend) Has(
	string,
) (bool, error) {
	return true, nil
}

func (b *previewColorBackend) List() ([]string, error) { return nil, nil }

func (b *previewColorBackend) Kill(
	string,
) error {
	return nil
}

// initGitRepo initializes a throwaway git repository at dir with one empty
// commit so worktree and remote resolution have something to read.
func initGitRepo(t *testing.T, dir string) {
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

// testModel builds a model over two workspaces, wsA and wsB, with wsA more
// recently used so it sorts first. It returns the model and the two workspace
// ids.
func testModel(t *testing.T) (Model, string, string) {
	t.Helper()
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	wsA, _ := reg.EnsureWorkspace("/repo-a", "", "repo-a")
	wsB, _ := reg.EnsureWorkspace("/repo-b", "", "repo-b")

	reg.AddSession(&registry.Session{
		ID: "a1", WorkspaceID: wsA.ID, Branch: "feat/a1",
	})
	reg.AddSession(&registry.Session{
		ID: "a2", WorkspaceID: wsA.ID, Branch: "feat/a2",
	})
	reg.AddSession(&registry.Session{
		ID: "b1", WorkspaceID: wsB.ID, Branch: "feat/b1",
	})

	wsA.LastUsedAt = time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	wsB.LastUsedAt = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	return New(t.TempDir(), reg, wsA.ID, config.Default()), wsA.ID, wsB.ID
}

func TestNewActivatesCurrentWorkspace(t *testing.T) {
	m, _, wsB := testModel(t)

	reg := m.reg
	m2 := New(t.TempDir(), reg, wsB, config.Default())
	if m2.activeID != wsB {
		t.Fatalf("activeID = %q, want current workspace %q", m2.activeID, wsB)
	}

	m3 := New(t.TempDir(), reg, "", config.Default())
	if m3.activeID != m.workspaces[0].ID {
		t.Fatalf(
			"activeID = %q, want MRU first %q",
			m3.activeID,
			m.workspaces[0].ID,
		)
	}
}

func TestSessionsFilteredByActiveTab(t *testing.T) {
	m, wsA, wsB := testModel(t)

	if got := len(m.sessions()); got != 2 {
		t.Fatalf("wsA sessions = %d, want 2", got)
	}
	if m.activeID != wsA {
		t.Fatalf("activeID = %q, want %q", m.activeID, wsA)
	}

	m.cycleTab(1)
	if m.activeID != wsB {
		t.Fatalf("after cycle activeID = %q, want %q", m.activeID, wsB)
	}
	if got := len(m.sessions()); got != 1 {
		t.Fatalf("wsB sessions = %d, want 1", got)
	}
}

func TestCycleTabWraps(t *testing.T) {
	m, wsA, wsB := testModel(t)

	m.cursor = 1
	m.cycleTab(1)
	if m.activeID != wsB {
		t.Fatalf("activeID = %q, want %q", m.activeID, wsB)
	}
	if m.cursor != 0 {
		t.Fatalf("cursor = %d, want reset to 0 on tab change", m.cursor)
	}

	m.cycleTab(1)
	if m.activeID != wsA {
		t.Fatalf(
			"cycle past end activeID = %q, want wrap to %q",
			m.activeID,
			wsA,
		)
	}
}

func TestRefreshFollowsActiveWorkspaceById(t *testing.T) {
	m, _, wsB := testModel(t)

	m.cycleTab(1)
	if m.activeID != wsB {
		t.Fatalf("precondition: activeID = %q, want %q", m.activeID, wsB)
	}

	wsBPtr, _ := m.reg.Workspace(wsB)
	wsBPtr.LastUsedAt = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	m.refresh()
	if m.activeID != wsB {
		t.Fatalf(
			"active tab moved on reorder: activeID = %q, want %q",
			m.activeID,
			wsB,
		)
	}
}

func TestRefreshClampsCursor(t *testing.T) {
	m, _, _ := testModel(t)
	m.cursor = 5
	m.refresh()
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want clamped to last index 1", m.cursor)
	}
}

func TestEnterCreatePreselectsDefaultProfile(t *testing.T) {
	m, _, _ := testModel(t)

	next, _ := m.enterCreate()
	got := next.(Model)
	if got.mode != modeCreate {
		t.Fatal("enterCreate did not switch to create mode")
	}
	prof := got.form.Params().Profile
	if prof == "" {
		t.Fatal("create form preselected no profile")
	}
	if want := registry.DefaultProfileName; prof != want {
		t.Fatalf("preselected profile = %q, want default %q", prof, want)
	}
}

func TestEnterCreateWithNoWorkspaceOpensPlainForm(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	m := New(t.TempDir(), reg, "", config.Default())
	if m.currentWorkspace() != nil {
		t.Fatal("precondition: expected no current workspace")
	}

	next, _ := m.enterCreate()
	got := next.(Model)
	if got.mode != modeCreate {
		t.Fatal("enterCreate did not open the form when there is no workspace")
	}
	params := got.form.Params()
	if params.Profile != "" {
		t.Fatalf(
			"form profile = %q, want none without a workspace",
			params.Profile,
		)
	}
	if params.Branch != "" {
		t.Fatalf(
			"default params carried a branch %q, want a plain session",
			params.Branch,
		)
	}
	if params.WorkingDir != "" {
		t.Fatalf(
			"directory field should start empty, got %q",
			params.WorkingDir,
		)
	}
}

// TestSubmitEmptyDefaultsToWorkingDir checks that submitting the create form
// with an empty directory and no branch falls back to a plain session in the
// current working directory rather than failing.
func TestSubmitEmptyDefaultsToWorkingDir(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	m := New(t.TempDir(), reg, "", config.Default())

	next, _ := m.enterCreate()
	m = next.(Model)

	next, cmd := m.updateCreate(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter emitted no form-submit command")
	}
	next, _ = next.(Model).Update(cmd())
	got := next.(Model)
	if got.mode != modeList {
		t.Fatalf("submit left mode = %v, want modeList", got.mode)
	}
	if got.status == "" {
		t.Fatal("submit with empty directory did not start creating a session")
	}
}

// TestWorktreeWorkspaceTargetsPickedDirRepo is the regression test for the bug:
// a worktree session must be created against the repository of the chosen
// Directory, not the active workspace tab. With repoA active and a Directory
// inside repoB, the resolved workspace must be repoB's — registered if new — and
// while repoB is still unregistered the form must offer its default profile
// rather than repoA's profiles.
func TestWorktreeWorkspaceTargetsPickedDirRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	repoA, repoB := t.TempDir(), t.TempDir()
	initGitRepo(t, repoA)
	initGitRepo(t, repoB)

	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	pathA, urlA, err := repo.Resolve(repoA)
	if err != nil {
		t.Fatalf("resolve repoA: %v", err)
	}
	wsA, _ := repo.Register(reg, pathA, urlA)

	m := New(t.TempDir(), reg, wsA.ID, config.Default())
	if cur := m.currentWorkspace(); cur == nil || cur.ID != wsA.ID {
		t.Fatal("precondition: repoA is not the active workspace tab")
	}

	next, _ := m.enterCreate()
	m = next.(Model)
	m.form.SetDir(repoB)

	if got := m.profilesFor(m.form.BranchRepo); len(got) != 1 ||
		got[0] != registry.DefaultProfileName {
		t.Fatalf(
			"profiles for unregistered repoB = %v, want [%s]",
			got,
			registry.DefaultProfileName,
		)
	}

	target, err := m.worktreeWorkspace()
	if err != nil {
		t.Fatalf("worktreeWorkspace: %v", err)
	}

	pathB, urlB, err := repo.Resolve(repoB)
	if err != nil {
		t.Fatalf("resolve repoB: %v", err)
	}
	wantID := registry.WorkspaceID(pathB, urlB)
	if target.ID != wantID {
		t.Fatalf(
			"worktree workspace id = %q, want picked repoB id %q",
			target.ID,
			wantID,
		)
	}
	if target.ID == wsA.ID {
		t.Fatal("worktree session targeted active tab repoA, not picked repoB")
	}
	if _, ok := reg.Workspace(wantID); !ok {
		t.Fatal("picked repoB workspace was not registered in the registry")
	}

	if got := validProfile(target, "repoA-only"); got != "" {
		t.Fatalf(
			"validProfile kept a name absent from the target workspace: %q",
			got,
		)
	}
}

func TestEnterWorkspaceAddOpensPicker(t *testing.T) {
	m, _, _ := testModel(t)

	next, _ := m.enterWorkspaceAdd()
	m = next.(Model)
	if m.mode != modePickWorkspace {
		t.Fatalf("mode = %v, want modePickWorkspace", m.mode)
	}
}

func TestAddWorkspaceRegistersNewTab(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	repoX := t.TempDir()
	initGitRepo(t, repoX)

	m, wsA, _ := testModel(t)
	before := len(m.reg.ListWorkspaces())

	next, _ := m.addWorkspace(repoX)
	m = next.(Model)

	if m.mode != modeList {
		t.Fatalf("mode = %v, want modeList after add", m.mode)
	}
	pathX, urlX, err := repo.Resolve(repoX)
	if err != nil {
		t.Fatalf("resolve repoX: %v", err)
	}
	wantID := registry.WorkspaceID(pathX, urlX)
	if m.activeID != wantID {
		t.Fatalf("activeID = %q, want new workspace %q", m.activeID, wantID)
	}
	if m.activeID == wsA {
		t.Fatal("active tab stayed on the old workspace, not the added one")
	}
	if m.cursor != 0 {
		t.Fatalf("cursor = %d, want reset to 0 on the new tab", m.cursor)
	}
	if got := len(m.reg.ListWorkspaces()); got != before+1 {
		t.Fatalf("workspace count = %d, want %d", got, before+1)
	}
	if _, ok := m.reg.Workspace(wantID); !ok {
		t.Fatal("added repo was not registered in the registry")
	}
	if got := len(m.sessions()); got != 0 {
		t.Fatalf("added workspace has %d sessions, want 0", got)
	}
}

func TestAddWorkspaceIsIdempotent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	repoX := t.TempDir()
	initGitRepo(t, repoX)

	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	pathX, urlX, err := repo.Resolve(repoX)
	if err != nil {
		t.Fatalf("resolve repoX: %v", err)
	}
	wsX, _ := repo.Register(reg, pathX, urlX)

	m := New(t.TempDir(), reg, wsX.ID, config.Default())
	before := len(m.reg.ListWorkspaces())

	next, _ := m.addWorkspace(repoX)
	m = next.(Model)

	if got := len(m.reg.ListWorkspaces()); got != before {
		t.Fatalf("workspace count = %d, want unchanged %d", got, before)
	}
	if m.activeID != wsX.ID {
		t.Fatalf("activeID = %q, want existing tab %q", m.activeID, wsX.ID)
	}
	if m.err != nil {
		t.Fatalf("re-adding surfaced an error: %v", m.err)
	}
}

func TestAddWorkspaceNonGitErrors(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	plain := t.TempDir()

	m, wsA, _ := testModel(t)
	before := len(m.reg.ListWorkspaces())

	next, _ := m.addWorkspace(plain)
	m = next.(Model)

	if m.err == nil {
		t.Fatal("non-git path did not surface an error")
	}
	if got := len(m.reg.ListWorkspaces()); got != before {
		t.Fatalf(
			"non-git add changed workspace count to %d, want %d",
			got,
			before,
		)
	}
	if m.activeID != wsA {
		t.Fatalf("active tab moved on a failed add: %q", m.activeID)
	}
}

func TestEnterWorkspaceDeleteOpensConfirm(t *testing.T) {
	m, _, _ := testModel(t)

	next, _ := m.enterWorkspaceDelete()
	m = next.(Model)
	if m.mode != modeConfirm {
		t.Fatalf("mode = %v, want modeConfirm", m.mode)
	}
	if m.confirmCmd == nil {
		t.Fatal("enterWorkspaceDelete armed no confirm command")
	}
}

func TestEnterWorkspaceDeleteNoWorkspaceIsNoop(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	m := New(t.TempDir(), reg, "", config.Default())

	next, cmd := m.enterWorkspaceDelete()
	m = next.(Model)
	if m.mode != modeList {
		t.Fatalf("mode = %v, want modeList (no-op) with no workspace", m.mode)
	}
	if cmd != nil {
		t.Fatal("enterWorkspaceDelete with no workspace returned a command")
	}
}

func TestWorkspaceDeleteCmdRemovesTabAndSessions(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo-x", "", "repo-x")
	reg.AddSession(&registry.Session{
		ID: "p1", WorkspaceID: ws.ID, WorkingDir: "/tmp", TmuxName: "wasa_p1",
	})

	m := New(t.TempDir(), reg, ws.ID, config.Default())
	m.tmux = &previewColorBackend{}

	msg := m.workspaceDeleteCmd(ws)()
	wd, ok := msg.(workspaceDeletedMsg)
	if !ok {
		t.Fatalf("msg type = %T, want workspaceDeletedMsg", msg)
	}
	if wd.err != nil {
		t.Fatalf("workspaceDeleteCmd error: %v", wd.err)
	}
	if _, ok := reg.Workspace(ws.ID); ok {
		t.Fatal("workspace still present after delete")
	}
	if got := len(reg.ListSessions()); got != 0 {
		t.Fatalf("sessions remaining = %d, want 0", got)
	}
}

func TestListCursorNavigation(t *testing.T) {
	m, _, _ := testModel(t)

	down := tea.KeyMsg{Type: tea.KeyDown}
	next, _ := m.updateList(down)
	m = next.(Model)
	if m.cursor != 1 {
		t.Fatalf("cursor after down = %d, want 1", m.cursor)
	}

	next, _ = m.updateList(down)
	m = next.(Model)
	if m.cursor != 1 {
		t.Fatalf("cursor clamped at last = %d, want 1", m.cursor)
	}

	up := tea.KeyMsg{Type: tea.KeyUp}
	next, _ = m.updateList(up)
	m = next.(Model)
	if m.cursor != 0 {
		t.Fatalf("cursor after up = %d, want 0", m.cursor)
	}
}

func TestEnterConfirmDeleteOpensModal(t *testing.T) {
	m, _, _ := testModel(t)

	next, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	got := next.(Model)
	if got.mode != modeConfirm {
		t.Fatal("d did not open the confirm modal")
	}
	if got.confirmCmd == nil {
		t.Fatal("d opened the modal without a pending delete command")
	}
}

func TestEnterConfirmDeleteNoSelectionIsNoop(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	m := New(t.TempDir(), reg, ws.ID, config.Default())

	next, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	got := next.(Model)
	if got.mode != modeList {
		t.Fatal("d opened a modal with no session selected")
	}
	if got.confirmCmd != nil {
		t.Fatal("confirmCmd set with no session selected")
	}
}

func TestConfirmCancelLeavesSessionUnchanged(t *testing.T) {
	m, _, _ := testModel(t)
	next, _ := m.enterConfirmDelete()
	m = next.(Model)

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyEsc},
		{Type: tea.KeyRunes, Runes: []rune("n")},
		{Type: tea.KeyRunes, Runes: []rune("q")},
	} {
		next, cmd := m.updateConfirm(key)
		if cmd == nil {
			t.Fatalf("cancel key %v emitted no result command", key)
		}
		next, _ = next.(Model).Update(cmd())
		got := next.(Model)
		if got.mode != modeList {
			t.Fatalf("cancel key %v did not return to the list", key)
		}
		if got.confirmCmd != nil {
			t.Fatalf("cancel key %v left a pending command", key)
		}
	}
	if _, ok := m.reg.Session("a1"); !ok {
		t.Fatal("cancel removed the session record")
	}
}

func TestConfirmDeleteRemovesExitedSession(t *testing.T) {
	m, _, _ := testModel(t)
	m.cursor = 1 // select a2; a1 stays so the cursor has a neighbour to land on

	a2, _ := m.reg.Session("a2")
	a2.Status = registry.StatusExited // exited path runs no backend

	next, _ := m.enterConfirmDelete()
	m = next.(Model)

	// y confirms directly regardless of which button is focused.
	next, cmd := m.updateConfirm(
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")},
	)
	m = next.(Model)
	if cmd == nil {
		t.Fatal("confirm produced no accept command")
	}

	// The accept message runs the stored delete command and returns to the list.
	next, cmd = m.Update(cmd())
	m = next.(Model)
	if m.mode != modeList {
		t.Fatal("confirm did not return to the list")
	}
	if cmd == nil {
		t.Fatal("accept did not run the stored delete command")
	}

	// The exited-session path runs no backend; the command removes the record
	// and Saves. Feeding its result back into Update refreshes and clamps.
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.err != nil {
		t.Fatalf("delete reported error: %v", m.err)
	}
	if _, ok := m.reg.Session("a2"); ok {
		t.Fatal("delete left the session record in the registry")
	}
	if got := len(m.sessions()); got != 1 {
		t.Fatalf("wsA sessions after delete = %d, want 1", got)
	}
	if m.cursor < 0 || m.cursor >= len(m.sessions()) {
		t.Fatalf("cursor %d out of range after delete", m.cursor)
	}
}

func TestConfirmEnterDefaultsToCancel(t *testing.T) {
	m, _, _ := testModel(t)
	next, _ := m.enterConfirmDelete()
	m = next.(Model)

	// The cancel button starts focused, so a stray enter must not delete.
	next, cmd := m.updateConfirm(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter emitted no result command")
	}
	next, cmd = next.(Model).Update(cmd())
	m = next.(Model)
	if m.mode != modeList {
		t.Fatal("enter did not close the modal")
	}
	if cmd != nil {
		t.Fatal("enter on the default (cancel) focus produced a command")
	}
	if _, ok := m.reg.Session("a1"); !ok {
		t.Fatal("enter on the default focus deleted the session")
	}
}

func TestConfirmFocusConfirmThenEnter(t *testing.T) {
	m, _, _ := testModel(t)
	a1, _ := m.reg.Session("a1")
	a1.Status = registry.StatusExited // exited path runs no backend

	next, _ := m.enterConfirmDelete()
	m = next.(Model)

	// Move focus onto the confirm button, then enter deletes.
	next, _ = m.updateConfirm(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	next, cmd := m.updateConfirm(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on the confirm button emitted no result command")
	}
	next, cmd = next.(Model).Update(cmd())
	m = next.(Model)
	if cmd == nil {
		t.Fatal("accept did not run the stored delete command")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if _, ok := m.reg.Session("a1"); ok {
		t.Fatal("tab+enter did not delete the session")
	}
}

func TestKillOpensConfirmForRunningSession(t *testing.T) {
	m, _, _ := testModel(t)
	if m.selectedSession().Status != registry.StatusRunning {
		t.Fatal("precondition: selected session should be running")
	}

	next, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	got := next.(Model)
	if got.mode != modeConfirm {
		t.Fatal("k did not open the confirm modal for a running session")
	}
	if got.confirmCmd == nil {
		t.Fatal("k opened the modal without a pending kill command")
	}
}

func TestKillExitedSessionIsNoop(t *testing.T) {
	m, _, _ := testModel(t)
	a1, _ := m.reg.Session("a1")
	a1.Status = registry.StatusExited

	next, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	got := next.(Model)
	if got.mode != modeList {
		t.Fatal("k opened a confirm modal for an already-exited session")
	}
}

func TestEmptyRegistryShowsBanner(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	m := New(t.TempDir(), reg, "", config.Default())
	if m.activeID != "" {
		t.Fatalf("activeID = %q, want empty with no workspaces", m.activeID)
	}

	m.width, m.height = 80, 24
	view := m.View()
	if !strings.Contains(view, "No workspaces yet.") {
		t.Fatalf("view missing empty-state banner:\n%s", view)
	}
	if !strings.Contains(view, "add a git repo") {
		t.Fatalf("banner does not point at workspace add:\n%s", view)
	}
}

// TestPreviewPreservesColor is the regression guard for issue #46 symptom 1:
// the cockpit preview must render the captured agent's ANSI colors, and the
// per-line width truncation must not slice through an escape sequence. The
// capture carries a truecolor SGR followed by long text that overflows the
// pane, so a correct (ANSI-aware) render keeps the full escape intact while a
// naive byte/rune truncation would cut it mid-code.
func TestPreviewPreservesColor(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	reg.AddSession(&registry.Session{
		ID: "s1", WorkspaceID: ws.ID, Branch: "feat/s1",
		Status: registry.StatusRunning, TmuxName: "wasa_s1",
	})

	m := New(t.TempDir(), reg, ws.ID, config.Default())
	m.width, m.height = 100, 30
	be := &previewColorBackend{
		content: "\x1b[38;2;255;0;0mRED" + strings.Repeat("x", 200) + "\x1b[0m",
	}
	m.tmux = be
	m.stream = nil
	m.tabbed.Preview = pane.NewPreview(nil, be)
	m.tabbed.Preview.PollOrReconnect(m.previewTarget())

	out := m.View()
	if !strings.Contains(out, "\x1b[38;2;255;0;0m") {
		t.Fatalf("preview dropped the truecolor escape; "+
			"colors are stripped or corrupted.\n%q", out)
	}
}

func TestSelectedSessionEmpty(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	m := New(t.TempDir(), reg, ws.ID, config.Default())

	if m.selectedSession() != nil {
		t.Fatal("selectedSession non-nil with no sessions")
	}
	if m.View() == "" {
		t.Fatal("empty workspace rendered nothing; want an empty-state banner")
	}
}
