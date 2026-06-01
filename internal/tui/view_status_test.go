package tui

import (
	"strings"
	"testing"

	"github.com/joakimcarlsson/wasa/internal/config"
	"github.com/joakimcarlsson/wasa/internal/registry"
)

// TestSessionListShowsRuntimeStatus renders a workspace whose running sessions
// hold each derived runtime status and asserts the list labels them distinctly,
// alongside the exited state read straight from the registry.
func TestSessionListShowsRuntimeStatus(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	for _, id := range []string{"work", "wait", "idle", "gone"} {
		reg.AddSession(&registry.Session{
			ID: id, WorkspaceID: ws.ID, Branch: "feat/" + id,
			Status: registry.StatusRunning, TmuxName: "t-" + id,
		})
	}
	gone, _ := reg.Session("gone")
	gone.Status = registry.StatusExited

	m := New(t.TempDir(), reg, ws.ID, config.Default())
	m.width, m.height = 120, 30
	m.statuses.obs["work"] = &paneObservation{status: statusWorking}
	m.statuses.obs["wait"] = &paneObservation{status: statusWaiting}
	m.statuses.obs["idle"] = &paneObservation{status: statusIdle}

	out := m.View()
	for _, label := range []string{"working", "waiting", "idle", "exited"} {
		if !strings.Contains(out, label) {
			t.Fatalf("list missing %q state label:\n%s", label, out)
		}
	}
}
