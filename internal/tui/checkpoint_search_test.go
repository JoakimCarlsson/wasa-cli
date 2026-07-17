package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/joakimcarlsson/wasa-cli/internal/config"
	"github.com/joakimcarlsson/wasa-cli/internal/record"
	"github.com/joakimcarlsson/wasa-cli/internal/registry"
)

// searchModel builds a model over one workspace and opens the checkpoint search
// overlay, so tests start with an active, focused input.
func searchModel(t *testing.T) Model {
	t.Helper()
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, _ := reg.EnsureWorkspace("/repo", "", "repo")

	m := New(t.TempDir(), reg, ws.ID, config.Default())
	m.width, m.height = 120, 30

	next, _ := m.updateList(tea.KeyPressMsg{Text: "/", Code: '/'})
	return next.(Model)
}

// typeSearch feeds a fragment into the open search input one keystroke at a time
// and returns the resulting model.
func typeSearch(t *testing.T, m Model, text string) Model {
	t.Helper()
	for _, r := range text {
		next, _ := m.updateCheckpointSearch(
			tea.KeyPressMsg{Text: string(r), Code: r},
		)
		m = next.(Model)
	}
	return m
}

func hit(id, branch, file, line string) record.SearchHit {
	return record.SearchHit{
		Entry: record.Entry{
			When: time.Unix(0, 0),
			Meta: record.Meta{SessionID: id, Branch: branch},
		},
		File:     file,
		LineText: line,
		Start:    0,
		End:      len(line),
	}
}

func TestSearchKeyOpensSearch(t *testing.T) {
	m := searchModel(t)
	if m.mode != modeCheckpointSearch || !m.checkpointSearch.active {
		t.Fatal("\"/\" did not open the checkpoint search overlay")
	}
}

func TestSearchTypingSchedulesDebouncedScan(t *testing.T) {
	m := searchModel(t)
	next, cmd := m.updateCheckpointSearch(
		tea.KeyPressMsg{Text: "r", Code: 'r'},
	)
	m = next.(Model)
	if !m.checkpointSearch.pending {
		t.Fatal("a non-empty query did not mark a scan pending")
	}
	if m.checkpointSearch.query != "r" {
		t.Fatalf("query = %q, want r", m.checkpointSearch.query)
	}
	if cmd == nil {
		t.Fatal("typing did not schedule a debounce tick")
	}
}

func TestSearchEmptyQueryClearsHits(t *testing.T) {
	m := searchModel(t)
	m = typeSearch(t, m, "retry")
	m.checkpointSearch.hits = []record.SearchHit{hit("s1", "b", "intent", "x")}

	next, _ := m.updateCheckpointSearch(tea.KeyPressMsg{Code: tea.KeyBackspace})
	for m.checkpointSearch.query != "" {
		m = next.(Model)
		next, _ = m.updateCheckpointSearch(
			tea.KeyPressMsg{Code: tea.KeyBackspace},
		)
	}
	m = next.(Model)

	if len(m.checkpointSearch.hits) != 0 {
		t.Fatalf(
			"emptying the query left %d hits",
			len(m.checkpointSearch.hits),
		)
	}
	if m.checkpointSearch.pending {
		t.Fatal("emptying the query left a scan pending")
	}
}

func TestSearchStaleResultIgnored(t *testing.T) {
	m := searchModel(t)
	m = typeSearch(t, m, "retry")
	gen := m.checkpointSearch.gen

	stale := ckptSearchResultMsg{
		gen:  gen - 1,
		hits: []record.SearchHit{hit("s1", "b", "intent", "x")},
	}
	next, _ := m.applyCheckpointSearchResult(stale)
	m = next.(Model)
	if len(m.checkpointSearch.hits) != 0 {
		t.Fatal("a superseded result was installed")
	}

	fresh := ckptSearchResultMsg{
		gen: gen,
		hits: []record.SearchHit{
			hit("s1", "task/retry", "intent", "retry logic"),
		},
	}
	next, _ = m.applyCheckpointSearchResult(fresh)
	m = next.(Model)
	if len(m.checkpointSearch.hits) != 1 {
		t.Fatalf(
			"current result not installed: %d hits",
			len(m.checkpointSearch.hits),
		)
	}
	if m.checkpointSearch.pending {
		t.Fatal("installing a result left the scan pending")
	}
}

func TestSearchTickSupersession(t *testing.T) {
	m := searchModel(t)
	m = typeSearch(t, m, "retry")
	gen := m.checkpointSearch.gen

	if _, cmd := m.checkpointSearchTick(gen - 1); cmd != nil {
		t.Fatal("a superseded tick launched a scan")
	}
	if _, cmd := m.checkpointSearchTick(gen); cmd == nil {
		t.Fatal("the current tick did not launch a scan")
	}
}

func TestSearchCursorClamped(t *testing.T) {
	m := searchModel(t)
	m = typeSearch(t, m, "retry")
	fresh := ckptSearchResultMsg{
		gen: m.checkpointSearch.gen,
		hits: []record.SearchHit{
			hit("s1", "b", "intent", "x"),
			hit("s2", "b", "transcript", "y"),
		},
	}
	next, _ := m.applyCheckpointSearchResult(fresh)
	m = next.(Model)

	for range 5 {
		next, _ = m.updateCheckpointSearch(tea.KeyPressMsg{Code: tea.KeyDown})
		m = next.(Model)
	}
	if m.checkpointSearch.cursor != 1 {
		t.Fatalf(
			"cursor = %d, want 1 (clamped to last hit)",
			m.checkpointSearch.cursor,
		)
	}
	for range 5 {
		next, _ = m.updateCheckpointSearch(tea.KeyPressMsg{Code: tea.KeyUp})
		m = next.(Model)
	}
	if m.checkpointSearch.cursor != 0 {
		t.Fatalf(
			"cursor = %d, want 0 (clamped to first hit)",
			m.checkpointSearch.cursor,
		)
	}
}

func TestSearchEnterNoopWithoutHits(t *testing.T) {
	m := searchModel(t)
	next, _ := m.updateCheckpointSearch(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.mode != modeCheckpointSearch {
		t.Fatal("enter with no results left the search overlay")
	}
}

func TestSearchNoRecordShowsRecordingState(t *testing.T) {
	m := searchModel(t)
	m = typeSearch(t, m, "retry")

	noRecord := ckptSearchResultMsg{
		gen: m.checkpointSearch.gen,
		err: record.ErrNoRecord,
	}
	next, _ := m.applyCheckpointSearchResult(noRecord)
	m = next.(Model)

	m.checkpointSearch.recording = false
	if body := m.checkpointSearchBody(80); !strings.Contains(
		body, "recording is off",
	) {
		t.Fatalf("recording-off body = %q, want the toggle hint", body)
	}

	m.checkpointSearch.recording = true
	if body := m.checkpointSearchBody(80); !strings.Contains(
		body, "no checkpoints recorded yet",
	) {
		t.Fatalf("recording-on empty body = %q, want the empty note", body)
	}
}

func TestSearchEscClosesToList(t *testing.T) {
	m := searchModel(t)
	next, _ := m.updateCheckpointSearch(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = next.(Model)
	if m.mode != modeList {
		t.Fatal("esc did not return to the session list")
	}
	if m.checkpointSearch.active {
		t.Fatal("esc left the search state active")
	}
}
