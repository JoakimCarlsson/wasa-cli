package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/joakimcarlsson/wasa-cli/internal/record"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/component"
)

// checkpointSearchDebounce is how long after the last keystroke the search runs,
// so typing fast spawns one record.Search rather than one per keystroke, and
// maxSearchRows bounds how many results are visible at once (each result draws as
// two lines) — the rest scroll under the cursor.
const (
	checkpointSearchDebounce = 150 * time.Millisecond
	maxSearchRows            = 8
	searchLimit              = 50
)

// checkpointSearchState backs the checkpoint search overlay (modeCheckpointSearch):
// a one-line query over the active workspace's record, its live results, and the
// cursor into them. It mirrors the CLI's `wasa checkpoints search`, driven by the
// same record.Search engine, and selecting a result opens the #108 browser on
// that checkpoint. The search runs off the update goroutine and debounced: a
// keystroke bumps gen and schedules a ckptSearchTickMsg, the tick spawns the
// search, and a ckptSearchResultMsg is applied only while its gen is still current
// — so a slow scan over a large repo never blocks typing or lands stale results.
//
// repoDir/wsName/recording are captured at open time: repoDir is the workspace
// searched, wsName labels the box, and recording only decides which empty-state
// message to show (off → point at the toggle) since ErrNoRecord cannot tell the
// two apart.
type checkpointSearchState struct {
	active    bool
	input     textinput.Model
	repoDir   string
	wsName    string
	recording bool

	query     string
	hits      []record.SearchHit
	cursor    int
	offset    int
	searchErr error

	gen     int
	pending bool
}

// ckptSearchTickMsg fires after the debounce interval to start a deferred search;
// gen identifies the keystroke that scheduled it so a superseded tick is ignored.
type ckptSearchTickMsg struct{ gen int }

// ckptSearchResultMsg carries a completed record.Search back to the overlay,
// tagged with the generation that requested it so a superseded result is ignored.
type ckptSearchResultMsg struct {
	gen  int
	hits []record.SearchHit
	err  error
}

// enterCheckpointSearch opens the search overlay over the active workspace's
// record, focusing a fresh one-line query. The orphan tab has no repo to search,
// so it stays in the list with a hint rather than opening an empty overlay. The
// query starts empty, so the overlay opens on a "type to search" prompt.
func (m Model) enterCheckpointSearch() (tea.Model, tea.Cmd) {
	ws := m.currentWorkspace()
	if ws == nil {
		m.status = "search: select a workspace first"
		return m, nil
	}

	in := textinput.New()
	in.Prompt = "> "
	in.Placeholder = "search intent & transcripts"
	in.CharLimit = 200
	in.SetWidth(max(m.checkpointSearchWidth()-4, 10))
	in.Focus()

	m.checkpointSearch = checkpointSearchState{
		active:    true,
		input:     in,
		repoDir:   ws.RepoPath,
		wsName:    ws.Name,
		recording: len(m.recording[m.activeID]) > 0,
	}
	m.mode = modeCheckpointSearch
	m.err = nil
	m.status = ""
	return m, textinput.Blink
}

// updateCheckpointSearch drives the overlay: esc closes back to the session list,
// enter opens the highlighted result in the checkpoints browser, up/down move the
// result cursor, and every other key edits the query — after which the debounced
// search is (re)scheduled. A non-key message (the cursor blink) is forwarded to
// the input.
func (m Model) updateCheckpointSearch(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		var cmd tea.Cmd
		m.checkpointSearch.input, cmd = m.checkpointSearch.input.Update(msg)
		return m, cmd
	}

	switch key.String() {
	case "esc":
		m.checkpointSearch = checkpointSearchState{}
		m.mode = modeList
		return m, m.afterListChange()
	case "enter":
		return m.openSelectedHit()
	case "up", "ctrl+p":
		if m.checkpointSearch.cursor > 0 {
			m.checkpointSearch.cursor--
			m.checkpointSearch.ensureVisible()
		}
		return m, nil
	case "down", "ctrl+n":
		if m.checkpointSearch.cursor < len(m.checkpointSearch.hits)-1 {
			m.checkpointSearch.cursor++
			m.checkpointSearch.ensureVisible()
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.checkpointSearch.input, cmd = m.checkpointSearch.input.Update(msg)
	return m, tea.Batch(cmd, m.onCheckpointQueryChange())
}

// onCheckpointQueryChange reacts to an edited query: it bumps the generation so
// any in-flight tick or result is superseded, clears the results when the query
// is empty, and otherwise schedules a debounced search, returning the tick that
// will start it.
func (m *Model) onCheckpointQueryChange() tea.Cmd {
	cs := &m.checkpointSearch
	cs.gen++
	cs.query = strings.TrimSpace(cs.input.Value())
	if cs.query == "" {
		cs.pending = false
		cs.hits = nil
		cs.searchErr = nil
		cs.cursor = 0
		cs.offset = 0
		return nil
	}
	cs.pending = true
	return checkpointSearchTick(cs.gen)
}

// checkpointSearchTick handles a debounce tick: if it is still the current
// generation it launches the off-goroutine record.Search, otherwise it is a
// superseded tick and dropped.
func (m Model) checkpointSearchTick(gen int) (tea.Model, tea.Cmd) {
	cs := m.checkpointSearch
	if gen != cs.gen || !cs.pending {
		return m, nil
	}
	return m, runCheckpointSearch(gen, cs.repoDir, cs.query)
}

// applyCheckpointSearchResult installs a completed search, ignoring a result
// whose generation has been superseded by newer typing. ErrNoRecord (missing ref
// / recording disabled) is kept as searchErr so the view can render the empty
// state rather than surfacing it as an error.
func (m Model) applyCheckpointSearchResult(
	msg ckptSearchResultMsg,
) (tea.Model, tea.Cmd) {
	if msg.gen != m.checkpointSearch.gen {
		return m, nil
	}
	cs := &m.checkpointSearch
	cs.pending = false
	cs.hits = msg.hits
	cs.searchErr = msg.err
	cs.cursor = 0
	cs.offset = 0
	return m, nil
}

// runCheckpointSearch runs record.Search off the update goroutine and returns its
// result tagged with gen.
func runCheckpointSearch(gen int, repoDir, query string) tea.Cmd {
	return func() tea.Msg {
		hits, err := record.Search(repoDir, record.SearchOpts{
			Query: query,
			Limit: searchLimit,
		})
		return ckptSearchResultMsg{gen: gen, hits: hits, err: err}
	}
}

func checkpointSearchTick(gen int) tea.Cmd {
	return tea.Tick(checkpointSearchDebounce, func(time.Time) tea.Msg {
		return ckptSearchTickMsg{gen: gen}
	})
}

// openSelectedHit opens the highlighted result in the checkpoints browser, focused
// on that session's checkpoint. Search and browser read the same workspace repo,
// so the session id is guaranteed to resolve. With no results it is a no-op.
func (m Model) openSelectedHit() (tea.Model, tea.Cmd) {
	cs := m.checkpointSearch
	if cs.cursor < 0 || cs.cursor >= len(cs.hits) {
		return m, nil
	}
	sessionID := cs.hits[cs.cursor].Meta.SessionID
	m.checkpointSearch = checkpointSearchState{}
	return m.openCheckpoints(sessionID)
}

// ensureVisible scrolls the result window so the cursor stays on screen.
func (cs *checkpointSearchState) ensureVisible() {
	if cs.cursor < cs.offset {
		cs.offset = cs.cursor
	}
	if cs.cursor >= cs.offset+maxSearchRows {
		cs.offset = cs.cursor - maxSearchRows + 1
	}
	if cs.offset < 0 {
		cs.offset = 0
	}
}

// checkpointSearchWidth is the width of the search box's content, a comfortable
// fraction of the terminal floored and capped so it stays readable on both narrow
// and very wide terminals.
func (m Model) checkpointSearchWidth() int {
	return max(min(m.width-8, 96), 40)
}

// checkpointSearchView renders the search overlay: the title and query input, the
// live result list (or the appropriate empty/searching/error state), and the
// footer hint, framed in the shared picker box so it floats over the session list.
func (m Model) checkpointSearchView() string {
	cs := m.checkpointSearch
	w := m.checkpointSearchWidth()

	var b strings.Builder
	b.WriteString(m.theme.TitleStyle.Render("Search checkpoints"))
	if cs.wsName != "" {
		b.WriteString(m.theme.DimStyle.Render("  " + cs.wsName))
	}
	b.WriteString("\n")
	b.WriteString(cs.input.View())
	b.WriteString("\n\n")
	b.WriteString(m.checkpointSearchBody(w))
	b.WriteString("\n\n")
	b.WriteString(m.theme.DimStyle.Render(m.checkpointSearchFooter()))
	return m.theme.PickerStyle.Render(b.String())
}

// checkpointSearchBody renders the result region: a searching note while a scan is
// in flight with nothing yet to show, the recording-off / nothing-recorded message
// when the repo has no record, an error line for any other failure, a prompt while
// the query is empty, a no-matches line, or the scrolled result rows.
func (m Model) checkpointSearchBody(w int) string {
	cs := m.checkpointSearch
	switch {
	case cs.pending && len(cs.hits) == 0:
		return m.theme.DimStyle.Render("  searching…")
	case errors.Is(cs.searchErr, record.ErrNoRecord):
		return m.theme.DimStyle.Render(m.recordEmptyMessage(cs.recording))
	case cs.searchErr != nil:
		return m.theme.ErrorStyle.Render(
			component.PadAnsi("  "+cs.searchErr.Error(), w),
		)
	case cs.query == "":
		return m.theme.DimStyle.Render(
			"  type to search intents and transcripts",
		)
	case len(cs.hits) == 0:
		return m.theme.DimStyle.Render("  no matches")
	}

	end := min(cs.offset+maxSearchRows, len(cs.hits))
	lines := make([]string, 0, (end-cs.offset)*2)
	for i := cs.offset; i < end; i++ {
		lines = append(lines, m.checkpointHitRow(cs.hits[i], i == cs.cursor, w))
	}
	return strings.Join(lines, "\n")
}

// checkpointHitRow renders one result as two lines: a meta line with the session
// id, branch, date and matched file, and a snippet line with the match span
// highlighted. The selected row takes the selection band on both lines (plain, so
// the band reads cleanly); an unselected row dims the meta and highlights only the
// matched span of the snippet.
func (m Model) checkpointHitRow(
	h record.SearchHit,
	current bool,
	w int,
) string {
	branch := h.Meta.Branch
	if branch == "" {
		branch = "—"
	}
	when := h.When.Local().Format("2006-01-02 15:04")
	meta := fmt.Sprintf("%s · %s · %s", branch, when, h.File)

	text, hs, he := record.Snippet(h.LineText, h.Start, h.End, max(w-4, 8))

	if current {
		head := fmt.Sprintf(" %s  %s", h.Meta.SessionID, meta)
		snip := "   " + text
		return m.theme.SelRowTitleStyle.Render(component.Pad("▌ "+head, w)) +
			"\n" +
			m.theme.SelRowDescStyle.Render(component.Pad(snip, w))
	}

	head := "  " + h.Meta.SessionID +
		m.theme.DimStyle.Render("  "+meta)
	snip := "   " + m.styledSnippet(text, hs, he)
	return component.PadAnsi(head, w) + "\n" + component.PadAnsi(snip, w)
}

// styledSnippet renders a snippet with its matched span in the theme's match
// style and the surrounding text dimmed, each segment styled separately so no
// style nests inside another (which the terminal would not resume cleanly).
func (m Model) styledSnippet(text string, hs, he int) string {
	if hs < 0 || he > len(text) || hs >= he {
		return m.theme.DimStyle.Render(text)
	}
	return m.theme.DimStyle.Render(text[:hs]) +
		m.theme.MatchStyle.Render(text[hs:he]) +
		m.theme.DimStyle.Render(text[he:])
}

// checkpointSearchFooter is the overlay's key hints and a match count while
// results are showing.
func (m Model) checkpointSearchFooter() string {
	cs := m.checkpointSearch
	if len(cs.hits) > 0 {
		return strconv.Itoa(len(cs.hits)) +
			" matches · ↑↓ move · ↵ open · esc"
	}
	return "type to search · ↑↓ move · ↵ open · esc"
}
