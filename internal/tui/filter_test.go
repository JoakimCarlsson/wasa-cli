package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/joakimcarlsson/wasa/internal/config"
	"github.com/joakimcarlsson/wasa/internal/registry"
)

// filterModel builds a model over one workspace with four sessions whose titles,
// branches and liveness differ, so a filter can be exercised against name and
// status independently.
func filterModel(t *testing.T) Model {
	t.Helper()
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")

	sessions := []struct {
		id, title, branch string
		running           bool
	}{
		{"s1", "alpha login", "feat/login", true},
		{"s2", "beta search", "feat/search", true},
		{"s3", "gamma logout", "fix/logout", false},
		{"s4", "delta cache", "feat/cache", false},
	}
	for _, s := range sessions {
		status := registry.StatusExited
		if s.running {
			status = registry.StatusRunning
		}
		reg.AddSession(&registry.Session{
			ID: s.id, WorkspaceID: ws.ID, Title: s.title,
			Branch: s.branch, Status: status, TmuxName: "t-" + s.id,
		})
	}

	m := New(t.TempDir(), reg, ws.ID, config.Default())
	m.width, m.height = 120, 30
	return m
}

// typeFilter feeds a fragment into the open filter input one keystroke at a time,
// the way real input arrives, and returns the resulting model.
func typeFilter(t *testing.T, m Model, text string) Model {
	t.Helper()
	for _, r := range text {
		next, _ := m.updateList(
			tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}},
		)
		m = next.(Model)
	}
	return m
}

func sessionIDs(ss []*registry.Session) []string {
	ids := make([]string, len(ss))
	for i, s := range ss {
		ids[i] = s.ID
	}
	return ids
}

func TestFilterKeyOpensFilter(t *testing.T) {
	m := filterModel(t)
	if m.filter.active {
		t.Fatal("precondition: filter should start inactive")
	}

	next, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = next.(Model)
	if !m.filter.active {
		t.Fatal("/ did not open the session filter")
	}
}

func TestFilterEnterIsNoopWithoutSessions(t *testing.T) {
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")
	m := New(t.TempDir(), reg, ws.ID, config.Default())

	next, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if next.(Model).filter.active {
		t.Fatal("filter opened over an empty session list")
	}
}

func TestFilterByName(t *testing.T) {
	m := filterModel(t)
	next, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = next.(Model)

	m = typeFilter(t, m, "login")
	got := sessionIDs(m.sessions())
	if len(got) != 1 || got[0] != "s1" {
		t.Fatalf("filter \"login\" = %v, want [s1]", got)
	}
}

func TestFilterMatchesBranch(t *testing.T) {
	m := filterModel(t)
	next, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = next.(Model)

	// "feat" appears only in the branches, not the titles.
	m = typeFilter(t, m, "feat")
	got := sessionIDs(m.sessions())
	want := map[string]bool{"s1": true, "s2": true, "s4": true}
	if len(got) != len(want) {
		t.Fatalf("filter \"feat\" = %v, want the three feat/* branches", got)
	}
	for _, id := range got {
		if !want[id] {
			t.Fatalf("filter \"feat\" returned %q, not a feat/* branch", id)
		}
	}
}

func TestFilterStatusTokenRunning(t *testing.T) {
	m := filterModel(t)
	next, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = next.(Model)

	m = typeFilter(t, m, "running")
	got := sessionIDs(m.sessions())
	want := map[string]bool{"s1": true, "s2": true}
	if len(got) != len(want) {
		t.Fatalf("filter \"running\" = %v, want the two running sessions", got)
	}
	for _, id := range got {
		if !want[id] {
			t.Fatalf("running token kept exited session %q", id)
		}
	}
}

func TestFilterStatusTokenExitedWithText(t *testing.T) {
	m := filterModel(t)
	next, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = next.(Model)

	// "exited log" narrows to exited sessions whose haystack matches "log":
	// s3 (gamma logout / fix/logout) qualifies; s1 matches "log" but is running.
	m = typeFilter(t, m, "exited log")
	got := sessionIDs(m.sessions())
	if len(got) != 1 || got[0] != "s3" {
		t.Fatalf("filter \"exited log\" = %v, want [s3]", got)
	}
}

func TestFilterEmptyResultShowsNoMatches(t *testing.T) {
	m := filterModel(t)
	next, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = next.(Model)

	m = typeFilter(t, m, "zzzznope")
	if got := len(m.sessions()); got != 0 {
		t.Fatalf("nonsense filter matched %d sessions, want 0", got)
	}
	if out := m.View(); !strings.Contains(out, "no matches") {
		t.Fatalf(
			"empty filter result did not show a no-matches state:\n%s",
			out,
		)
	}
}

func TestFilterClampsCursorIntoNarrowedSet(t *testing.T) {
	m := filterModel(t)
	m.cursor = 3 // last session in the full list
	next, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = next.(Model)

	m = typeFilter(t, m, "login") // narrows to one match
	if got := len(m.sessions()); got != 1 {
		t.Fatalf("precondition: filter should leave 1 match, got %d", got)
	}
	if m.cursor != 0 {
		t.Fatalf(
			"cursor = %d, want clamped to 0 within the narrowed set",
			m.cursor,
		)
	}
	if s := m.selectedSession(); s == nil || s.ID != "s1" {
		t.Fatalf("selectedSession = %v, want the single match s1", s)
	}
}

func TestFilterEscRestoresFullList(t *testing.T) {
	m := filterModel(t)
	next, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = next.(Model)
	m = typeFilter(t, m, "login")
	if len(m.sessions()) != 1 {
		t.Fatalf(
			"precondition: filter should narrow to 1, got %d",
			len(m.sessions()),
		)
	}

	next, _ = m.updateList(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.filter.active {
		t.Fatal("esc did not close the filter")
	}
	if got := len(m.sessions()); got != 4 {
		t.Fatalf("after esc sessions = %d, want the full 4", got)
	}
	if m.cursor != 0 {
		t.Fatalf("after esc cursor = %d, want 0", m.cursor)
	}
}

func TestParseFilterQuery(t *testing.T) {
	cases := []struct {
		raw, status, text string
	}{
		{"", "", ""},
		{"login", "", "login"},
		{"running", tokenRunning, ""},
		{"exited", tokenExited, ""},
		{"running login", tokenRunning, "login"},
		{"Exited  Log", tokenExited, "Log"},
		{"run", "", "run"},
	}
	for _, c := range cases {
		status, text := parseFilterQuery(c.raw)
		if status != c.status || text != c.text {
			t.Errorf(
				"parseFilterQuery(%q) = (%q, %q), want (%q, %q)",
				c.raw, status, text, c.status, c.text,
			)
		}
	}
}
