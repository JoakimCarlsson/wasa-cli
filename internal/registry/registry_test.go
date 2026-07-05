package registry

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func newTestRegistry(t *testing.T) (*Registry, *time.Time) {
	t.Helper()
	reg, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	clock := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	reg.now = func() time.Time { return clock }
	return reg, &clock
}

// TestLoadPreBaseCommitState is the back-compatibility guard for the new
// optional BaseCommit field: a registry written before it existed (a worktree
// session JSON with no baseCommit key) must load without error and leave
// BaseCommit empty, so an upgrade does not invalidate persisted state.
func TestLoadPreBaseCommitState(t *testing.T) {
	dir := t.TempDir()
	old := `{
  "workspaces": [],
  "sessions": [
    {
      "id": "s1",
      "branch": "feature/x",
      "worktreePath": "/wt/x",
      "tmuxName": "wasa_a_s1",
      "status": "running",
      "createdAt": "2026-01-01T00:00:00Z"
    }
  ]
}`
	if err := os.WriteFile(
		filepath.Join(dir, fileName), []byte(old), 0o644,
	); err != nil {
		t.Fatal(err)
	}

	reg, err := Open(dir)
	if err != nil {
		t.Fatalf("Open pre-baseCommit state: %v", err)
	}
	s, ok := reg.Session("s1")
	if !ok {
		t.Fatal("session not loaded from pre-baseCommit state")
	}
	if s.BaseCommit != "" {
		t.Fatalf("BaseCommit = %q, want empty for a pre-upgrade session",
			s.BaseCommit)
	}
}

func TestEmptyEnumeration(t *testing.T) {
	reg, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	ws := reg.ListWorkspaces()
	if ws == nil {
		t.Fatal("ListWorkspaces returned nil, want non-nil empty slice")
	}
	if len(ws) != 0 {
		t.Fatalf("ListWorkspaces len = %d, want 0", len(ws))
	}

	sess := reg.ListSessions()
	if sess == nil {
		t.Fatal("ListSessions returned nil, want non-nil empty slice")
	}
	if len(sess) != 0 {
		t.Fatalf("ListSessions len = %d, want 0", len(sess))
	}
}

func TestRemoveWorkspace(t *testing.T) {
	reg, _ := newTestRegistry(t)

	a, _ := reg.EnsureWorkspace("/a", "", "a")
	b, _ := reg.EnsureWorkspace("/b", "", "b")

	if !reg.RemoveWorkspace(a.ID) {
		t.Fatal("RemoveWorkspace reported false for a present workspace")
	}
	if reg.RemoveWorkspace(a.ID) {
		t.Fatal("RemoveWorkspace reported true for an absent workspace")
	}
	if _, ok := reg.Workspace(a.ID); ok {
		t.Fatal("removed workspace is still resolvable")
	}
	if _, ok := reg.Workspace(b.ID); !ok {
		t.Fatal("RemoveWorkspace removed the wrong workspace")
	}
	if got := reg.ListWorkspaces(); len(got) != 1 {
		t.Fatalf("workspace count = %d, want 1", len(got))
	}
}

func TestEnsureWorkspaceRegistersOnce(t *testing.T) {
	reg, _ := newTestRegistry(t)

	ws, created := reg.EnsureWorkspace("/repo", "remote", "repo")
	if !created {
		t.Fatal("first EnsureWorkspace did not report created")
	}
	if len(ws.Profiles) != 1 || ws.Profiles[0].Name != DefaultProfileName {
		t.Fatalf(
			"workspace profiles = %+v, want one default profile",
			ws.Profiles,
		)
	}

	again, created := reg.EnsureWorkspace("/repo", "remote", "repo")
	if created {
		t.Fatal("second EnsureWorkspace reported created for a known repo")
	}
	if again.ID != ws.ID {
		t.Fatalf("EnsureWorkspace id = %q, want %q", again.ID, ws.ID)
	}
	if got := reg.ListWorkspaces(); len(got) != 1 {
		t.Fatalf("workspace count = %d, want 1", len(got))
	}
}

func TestListWorkspacesMRU(t *testing.T) {
	reg, clock := newTestRegistry(t)

	*clock = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reg.EnsureWorkspace("/a", "", "a")
	*clock = time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	reg.EnsureWorkspace("/b", "", "b")
	*clock = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	reg.EnsureWorkspace("/c", "", "c")

	got := names(reg.ListWorkspaces())
	want := []string{"c", "b", "a"}
	if !slices.Equal(got, want) {
		t.Fatalf("MRU order = %v, want %v", got, want)
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()

	reg, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	clock := time.Date(2026, 5, 30, 8, 0, 0, 0, time.UTC)
	reg.now = func() time.Time { return clock }

	ws, _ := reg.EnsureWorkspace("/repo", "remote", "repo")
	reg.AddSession(&Session{
		ID:          "sess1",
		WorkspaceID: ws.ID,
		Branch:      "feature/x",
		TmuxName:    TmuxName(ws.ID, "sess1"),
	})
	if err := reg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}

	gotWS := reloaded.ListWorkspaces()
	if len(gotWS) != 1 || gotWS[0].ID != ws.ID || gotWS[0].Name != "repo" {
		t.Fatalf("reloaded workspaces = %+v", gotWS)
	}
	gotSess := reloaded.ListSessions()
	if len(gotSess) != 1 || gotSess[0].ID != "sess1" ||
		gotSess[0].Branch != "feature/x" {
		t.Fatalf("reloaded sessions = %+v", gotSess)
	}
}

func TestReconcileMarksExited(t *testing.T) {
	reg, _ := newTestRegistry(t)
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")

	reg.AddSession(
		&Session{ID: "alive", WorkspaceID: ws.ID, TmuxName: "wasa_alive"},
	)
	reg.AddSession(
		&Session{ID: "gone", WorkspaceID: ws.ID, TmuxName: "wasa_gone"},
	)

	changed := reg.Reconcile(func(name string) (bool, error) {
		return name == "wasa_alive", nil
	})
	if !changed {
		t.Fatal("Reconcile reported no change, want change")
	}

	for _, s := range reg.ListSessions() {
		switch s.ID {
		case "alive":
			if s.Status != StatusRunning {
				t.Fatalf(
					"alive session status = %q, want %q",
					s.Status,
					StatusRunning,
				)
			}
		case "gone":
			if s.Status != StatusExited {
				t.Fatalf(
					"gone session status = %q, want %q",
					s.Status,
					StatusExited,
				)
			}
		}
	}
}

func TestMarkExited(t *testing.T) {
	reg, _ := newTestRegistry(t)
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	reg.AddSession(&Session{ID: "s", WorkspaceID: ws.ID, TmuxName: "wasa_s"})

	if !reg.MarkExited("s") {
		t.Fatal("MarkExited did not find session")
	}
	if got := reg.ListSessions()[0].Status; got != StatusExited {
		t.Fatalf("status after MarkExited = %q, want %q", got, StatusExited)
	}
	if reg.MarkExited("missing") {
		t.Fatal("MarkExited reported finding an unknown session")
	}
}

func TestMarkExitedDoesNotTouchLastUsedAt(t *testing.T) {
	reg, clock := newTestRegistry(t)

	*clock = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	reg.AddSession(&Session{ID: "s", WorkspaceID: ws.ID})
	before := ws.LastUsedAt

	*clock = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	reg.MarkExited("s")
	if !ws.LastUsedAt.Equal(before) {
		t.Fatalf(
			"MarkExited advanced LastUsedAt: %v != %v",
			ws.LastUsedAt,
			before,
		)
	}
}

func TestMarkPaused(t *testing.T) {
	reg, _ := newTestRegistry(t)
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	reg.AddSession(&Session{ID: "s", WorkspaceID: ws.ID, TmuxName: "wasa_s"})

	if !reg.MarkPaused("s") {
		t.Fatal("MarkPaused did not find session")
	}
	if got := reg.ListSessions()[0].Status; got != StatusPaused {
		t.Fatalf("status after MarkPaused = %q, want %q", got, StatusPaused)
	}
	if _, ok := reg.Session("s"); !ok {
		t.Fatal("session record removed on pause; it must be retained")
	}
	if reg.MarkPaused("missing") {
		t.Fatal("MarkPaused reported finding an unknown session")
	}
}

// TestReconcileLeavesPausedAlone asserts that a paused session — which by
// definition has no tmux session — is never downgraded to exited by the
// startup reconcile.
func TestReconcileLeavesPausedAlone(t *testing.T) {
	reg, _ := newTestRegistry(t)
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	reg.AddSession(&Session{ID: "p", WorkspaceID: ws.ID, TmuxName: "wasa_p"})
	reg.MarkPaused("p")

	changed := reg.Reconcile(func(string) (bool, error) {
		return false, nil
	})
	if changed {
		t.Fatal("Reconcile changed a paused session")
	}
	if got := reg.ListSessions()[0].Status; got != StatusPaused {
		t.Fatalf("paused session reconciled to %q, want %q", got, StatusPaused)
	}
}

func TestReconcileIgnoresProbeError(t *testing.T) {
	reg, _ := newTestRegistry(t)
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	reg.AddSession(&Session{ID: "s", WorkspaceID: ws.ID, TmuxName: "wasa_s"})

	changed := reg.Reconcile(func(string) (bool, error) {
		return false, errTmuxMissing
	})
	if changed {
		t.Fatal("Reconcile changed a session despite a probe error")
	}
	if reg.ListSessions()[0].Status != StatusRunning {
		t.Fatal("session status changed despite a probe error")
	}
}

func TestLastUsedAtUpdatesOnCreateAndAttach(t *testing.T) {
	reg, clock := newTestRegistry(t)

	*clock = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	registered := ws.LastUsedAt

	*clock = time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	reg.AddSession(&Session{ID: "s", WorkspaceID: ws.ID})
	if !ws.LastUsedAt.Equal(*clock) {
		t.Fatalf("LastUsedAt after create = %v, want %v", ws.LastUsedAt, *clock)
	}
	if ws.LastUsedAt.Equal(registered) {
		t.Fatal("session create did not advance LastUsedAt")
	}

	*clock = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if !reg.MarkAttached("s") {
		t.Fatal("MarkAttached did not find session")
	}
	if !ws.LastUsedAt.Equal(*clock) {
		t.Fatalf("LastUsedAt after attach = %v, want %v", ws.LastUsedAt, *clock)
	}
}

func TestEnumerationDoesNotTouchLastUsedAt(t *testing.T) {
	reg, clock := newTestRegistry(t)

	*clock = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	before := ws.LastUsedAt

	*clock = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	reg.ListWorkspaces()
	reg.Workspace(ws.ID)

	if !ws.LastUsedAt.Equal(before) {
		t.Fatalf(
			"LastUsedAt changed by enumeration: %v != %v",
			ws.LastUsedAt,
			before,
		)
	}
}

func TestSelectProfile(t *testing.T) {
	w := &Workspace{Profiles: []Profile{
		{Name: "work"},
		{Name: "personal"},
	}}

	if p, err := w.SelectProfile(""); err != nil || p.Name != "work" {
		t.Fatalf(
			"SelectProfile(\"\") = (%+v, %v), want default \"work\"",
			p,
			err,
		)
	}
	if p, err := w.SelectProfile("personal"); err != nil ||
		p.Name != "personal" {
		t.Fatalf(
			"SelectProfile(personal) = (%+v, %v), want \"personal\"",
			p,
			err,
		)
	}
	if _, err := w.SelectProfile("nope"); err == nil {
		t.Fatal("SelectProfile of an unknown name returned nil error")
	}

	empty := &Workspace{}
	if _, ok := empty.DefaultProfile(); ok {
		t.Fatal("DefaultProfile reported ok for a profile-less workspace")
	}
	if _, err := empty.SelectProfile(""); err == nil {
		t.Fatal("SelectProfile on a profile-less workspace returned nil error")
	}
}

func TestEnvFileSecretsNeverPersisted(t *testing.T) {
	dir := t.TempDir()

	secretFile := filepath.Join(dir, "secret.env")
	if err := os.WriteFile(
		secretFile, []byte("TOKEN=secret-value\n"), 0o600,
	); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	reg, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	clock := time.Date(2026, 5, 30, 8, 0, 0, 0, time.UTC)
	reg.now = func() time.Time { return clock }

	ws, _ := reg.EnsureWorkspace("/repo", "remote", "repo")
	ws.Profiles = []Profile{{
		Name:     "work",
		Env:      map[string]string{"PUBLIC": "ok"},
		EnvFiles: []string{secretFile},
	}}
	if err := reg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	state := string(data)

	if strings.Contains(state, "secret-value") {
		t.Fatal("state JSON inlined an env-file secret")
	}
	if !strings.Contains(state, filepath.Base(secretFile)) {
		t.Fatal("state JSON does not store the env-file path")
	}

	reloaded, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, _ := reloaded.Workspace(ws.ID)
	if len(got.Profiles) != 1 ||
		!slices.Equal(got.Profiles[0].EnvFiles, []string{secretFile}) {
		t.Fatalf(
			"reloaded profile = %+v, want only the env-file path",
			got.Profiles,
		)
	}
}

func TestSessionLookupAndRemove(t *testing.T) {
	reg, _ := newTestRegistry(t)
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	reg.AddSession(&Session{ID: "keep", WorkspaceID: ws.ID})
	reg.AddSession(&Session{ID: "drop", WorkspaceID: ws.ID})

	if _, ok := reg.Session("drop"); !ok {
		t.Fatal("Session did not find a present session")
	}
	if _, ok := reg.Session("absent"); ok {
		t.Fatal("Session found an absent session")
	}

	if !reg.RemoveSession("drop") {
		t.Fatal("RemoveSession did not find the session")
	}
	if _, ok := reg.Session("drop"); ok {
		t.Fatal("removed session is still resolvable")
	}
	if reg.RemoveSession("drop") {
		t.Fatal("RemoveSession reported finding an already-removed session")
	}

	got := reg.ListSessions()
	if len(got) != 1 || got[0].ID != "keep" {
		t.Fatalf("sessions after remove = %+v, want only keep", got)
	}
}

func TestRemoveSessionPersists(t *testing.T) {
	dir := t.TempDir()
	reg, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	reg.AddSession(&Session{ID: "gone", WorkspaceID: ws.ID})
	if !reg.RemoveSession("gone") {
		t.Fatal("RemoveSession did not find the session")
	}
	if err := reg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := reloaded.ListSessions(); len(got) != 0 {
		t.Fatalf("reloaded sessions = %+v, want none", got)
	}
}

func names(ws []*Workspace) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.Name
	}
	return out
}

var errTmuxMissing = errTmux("tmux not found")

type errTmux string

func (e errTmux) Error() string { return string(e) }
