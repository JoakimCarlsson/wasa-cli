package tui

import (
	"sort"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/joakimcarlsson/wasa-cli/internal/registry"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/component"
)

// maxGlobalRows is how many global-jump results are visible at once; the rest
// scroll under the cursor.
const maxGlobalRows = 12

// globalMatch is one global-jump candidate: the session, the name of the
// workspace that owns it (the qualifier the row shows, so identically named
// sessions in different repos stay distinguishable) and its fuzzy score.
type globalMatch struct {
	session *registry.Session
	wsID    string
	wsName  string
	score   int
}

// globalFilterState backs the global jump overlay (modeGlobalFilter): a one-line
// fuzzy query over every session in every workspace, whose result switches the
// active workspace and lands the cursor on that session. It is a sibling of the
// per-workspace filterState, not a mode of it — the two never share state — and
// it reuses the same query grammar (parseFilterQuery / matchesFilter) and the
// same fuzzy scorer, only over a wider candidate set. Matching is in-memory over
// the registry snapshot, so it runs synchronously on each keystroke.
type globalFilterState struct {
	active  bool
	input   textinput.Model
	matches []globalMatch
	cursor  int
	offset  int
}

// enterGlobalFilter opens the global jump overlay, focusing a fresh query input
// over every session the registry knows. With no sessions anywhere there is
// nothing to jump to, so it reports that in the status line and stays in the
// list.
func (m Model) enterGlobalFilter() (tea.Model, tea.Cmd) {
	if len(m.reg.ListSessions()) == 0 {
		m.status = "jump: no sessions yet"
		return m, nil
	}

	in := textinput.New()
	in.Prompt = "> "
	in.Placeholder = "jump to any session — prefix running/exited/paused"
	in.CharLimit = 200
	in.SetWidth(max(m.overlayWidth()-4, 10))
	in.Focus()

	m.globalFilter = globalFilterState{active: true, input: in}
	m.globalFilter.matches = m.globalCandidates("")
	m.mode = modeGlobalFilter
	m.err = nil
	m.status = ""
	return m, textinput.Blink
}

// exitGlobalFilter closes the overlay without touching the active workspace or
// the list cursor, so a cancelled jump leaves the cockpit exactly as it was.
func (m Model) exitGlobalFilter() (tea.Model, tea.Cmd) {
	m.globalFilter = globalFilterState{}
	m.mode = modeList
	return m, m.afterListChange()
}

// updateGlobalFilter routes input while the global jump is open. esc cancels,
// enter jumps to the highlighted session, up/down move within the results, and
// every other key edits the query — after which the candidate set is recomputed
// and the result cursor reset to the best match.
func (m Model) updateGlobalFilter(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		var cmd tea.Cmd
		m.globalFilter.input, cmd = m.globalFilter.input.Update(msg)
		return m, cmd
	}

	switch key.String() {
	case "esc":
		return m.exitGlobalFilter()
	case "ctrl+c":
		m.tabbed.Preview.Close()
		m.tabbed.Terminal.Close(m.tmux)
		return m, tea.Quit
	case "enter":
		return m.jumpToSelectedMatch()
	case "up", "ctrl+p":
		if m.globalFilter.cursor > 0 {
			m.globalFilter.cursor--
			m.globalFilter.ensureVisible()
		}
		return m, nil
	case "down", "ctrl+n":
		if m.globalFilter.cursor < len(m.globalFilter.matches)-1 {
			m.globalFilter.cursor++
			m.globalFilter.ensureVisible()
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.globalFilter.input, cmd = m.globalFilter.input.Update(msg)
	m.globalFilter.matches = m.globalCandidates(m.globalFilter.input.Value())
	m.globalFilter.cursor = 0
	m.globalFilter.offset = 0
	return m, cmd
}

// globalCandidates is the global jump's candidate set: every session in the
// registry that passes the parsed query, each carrying its owning workspace so
// the row can qualify it and the jump can switch to it. Orphan sessions carry
// the synthetic orphan tab's name, the tab they live on. Results are ordered by
// fuzzy score, best first, with ties broken by workspace then title so the list
// is stable between keystrokes.
func (m Model) globalCandidates(raw string) []globalMatch {
	status, text := parseFilterQuery(raw)
	all := m.reg.ListSessions()
	out := make([]globalMatch, 0, len(all))
	for _, s := range all {
		if !matchesFilter(s, status, text) {
			continue
		}
		score, _, _ := component.FuzzyScore(text, sessionHaystack(s))
		out = append(out, globalMatch{
			session: s,
			wsID:    s.WorkspaceID,
			wsName:  m.workspaceName(s.WorkspaceID),
			score:   score,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		if out[i].wsName != out[j].wsName {
			return out[i].wsName < out[j].wsName
		}
		ti, _ := sessionLabel(out[i].session)
		tj, _ := sessionLabel(out[j].session)
		return ti < tj
	})
	return out
}

// workspaceName is the display name of the workspace a session belongs to, or
// the orphan tab's name for a session that belongs to none. A workspace that has
// gone missing from the registry falls back to the orphan label too, so a row
// always reads as some tab.
func (m Model) workspaceName(wsID string) string {
	if wsID == "" {
		return orphanTabName
	}
	if ws, ok := m.reg.Workspace(wsID); ok {
		return ws.Name
	}
	return orphanTabName
}

// jumpToSelectedMatch accepts the highlighted result: it switches the active tab
// to the match's workspace, lands the list cursor on that session and tears the
// overlay down. The per-workspace filter is cleared as well, so the destination
// list is never left narrowed by a query the user typed for the jump. With no
// results it is a no-op.
func (m Model) jumpToSelectedMatch() (tea.Model, tea.Cmd) {
	gf := m.globalFilter
	if gf.cursor < 0 || gf.cursor >= len(gf.matches) {
		return m, nil
	}
	match := gf.matches[gf.cursor]

	m.globalFilter = globalFilterState{}
	m.filter = filterState{}
	m.mode = modeList
	m.activeID = match.wsID
	m.cursor = 0
	for i, s := range m.workspaceSessions() {
		if s.ID == match.session.ID {
			m.cursor = i
			break
		}
	}
	return m, m.afterListChange()
}

// ensureVisible scrolls the result window so the cursor stays on screen.
func (gf *globalFilterState) ensureVisible() {
	if gf.cursor < gf.offset {
		gf.offset = gf.cursor
	}
	if gf.cursor >= gf.offset+maxGlobalRows {
		gf.offset = gf.cursor - maxGlobalRows + 1
	}
	if gf.offset < 0 {
		gf.offset = 0
	}
}

// globalFilterView renders the jump overlay: the title and query input, the
// scrolled result rows (or the empty state), and the footer hint, framed in the
// shared picker box so it floats over the session list.
func (m Model) globalFilterView() string {
	gf := m.globalFilter
	w := m.overlayWidth()

	var b strings.Builder
	b.WriteString(m.theme.TitleStyle.Render("Jump to session"))
	b.WriteString(m.theme.DimStyle.Render("  all workspaces"))
	b.WriteString("\n")
	b.WriteString(gf.input.View())
	b.WriteString("\n\n")
	b.WriteString(m.globalFilterBody(w))
	b.WriteString("\n\n")
	b.WriteString(m.theme.DimStyle.Render(m.globalFilterFooter()))
	return m.theme.PickerStyle.Render(b.String())
}

// globalFilterBody renders the result region: a no-matches line, or the scrolled
// result rows.
func (m Model) globalFilterBody(w int) string {
	gf := m.globalFilter
	if len(gf.matches) == 0 {
		return m.theme.DimStyle.Render("  no matches")
	}
	end := min(gf.offset+maxGlobalRows, len(gf.matches))
	lines := make([]string, 0, end-gf.offset)
	for i := gf.offset; i < end; i++ {
		lines = append(
			lines,
			m.globalMatchRow(gf.matches[i], i == gf.cursor, w),
		)
	}
	return strings.Join(lines, "\n")
}

// globalMatchRow renders one result as a single line: the status dot, the
// workspace qualifier, the session title and its ref. The selected row takes the
// selection band plain — as the session list does — while an unselected row dims
// the qualifier and lights the fuzzy-matched characters of the title and ref.
func (m Model) globalMatchRow(match globalMatch, current bool, w int) string {
	title, ref := sessionLabel(match.session)
	dot := statusDot(m.theme, m.runtimeStatus(match.session))

	if current {
		row := " " + dot + " " + match.wsName + " · " + title + "  " + ref
		return m.theme.SelRowTitleStyle.Render(component.PadAnsi("▌"+row, w))
	}

	_, text := parseFilterQuery(m.globalFilter.input.Value())
	title, ref = m.highlightFuzzy(title, ref, text)
	row := "  " + dot + " " +
		m.theme.DimStyle.Render(match.wsName+" · ") + title +
		m.theme.DimStyle.Render("  "+ref)
	return component.PadAnsi(row, w)
}

// globalFilterFooter is the overlay's key hints, with a match count once results
// are showing.
func (m Model) globalFilterFooter() string {
	if n := len(m.globalFilter.matches); n > 0 {
		return strconv.Itoa(n) + " matches · ↑↓ move · ↵ jump · esc"
	}
	return "type to jump · ↑↓ move · ↵ jump · esc"
}
