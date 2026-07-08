package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/joakimcarlsson/wasa-cli/internal/config"
	"github.com/joakimcarlsson/wasa-cli/internal/record"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/component"
)

// checkpointsState backs the checkpoints view: the workspace repo it reads, the
// listed checkpoints (newest first, one per recorded session — record.List's
// order), the list cursor, and the scrollable detail of the selected checkpoint.
// It is built fresh each time the view opens (enterCheckpoints) and discarded on
// close, so it never has to converge with a live registry the way the session
// list does — the record is immutable history.
//
// recording is the active workspace's derived recording state at open time; it
// only decides which empty-state message to show (off → point at the toggle, on
// → "nothing recorded yet"), since record.List cannot itself tell the two apart.
type checkpointsState struct {
	repoDir   string
	wsName    string
	recording bool

	entries []record.Entry
	cursor  int
	listErr error

	// The detail of entries[cursor], loaded lazily on each selection change via
	// record.Find. vp holds the intent, meta and rendered transcript as one
	// scrollable body so a large transcript scrolls within the pane and can never
	// grow the surrounding layout. intent/transcript are kept so the body can be
	// re-wrapped on resize without re-reading git.
	vp         viewport.Model
	intent     string
	transcript string
	captured   bool
	loadErr    error
}

// newTranscriptViewport builds the detail viewport with the same keymap the diff
// pane uses: it scrolls with PageUp/PageDown and the ctrl+f/b/u/d chords and
// leaves the bare arrow keys to the checkpoint list, so up/down keep moving the
// list cursor (which re-targets the detail) rather than scrolling the transcript.
func newTranscriptViewport() viewport.Model {
	vp := viewport.New(0, 0)
	vp.KeyMap = viewport.KeyMap{
		PageDown:     key.NewBinding(key.WithKeys("pgdown", "ctrl+f")),
		PageUp:       key.NewBinding(key.WithKeys("pgup", "ctrl+b")),
		HalfPageDown: key.NewBinding(key.WithKeys("ctrl+d")),
		HalfPageUp:   key.NewBinding(key.WithKeys("ctrl+u")),
	}
	return vp
}

// enterCheckpoints opens the checkpoints view over the active workspace's repo,
// reading the checkpoint list up front (record.List is a single for-each-ref, the
// same call refreshRecording already runs each tick). It opens focused on the
// selected session's checkpoint when that session produced one, so jumping in
// from a recorded row lands on that row's record. The orphan tab has no repo to
// read, so it stays in the list with a hint rather than opening an empty view.
func (m Model) enterCheckpoints() (tea.Model, tea.Cmd) {
	ws := m.currentWorkspace()
	if ws == nil {
		m.status = "checkpoints: select a workspace first"
		return m, nil
	}

	cs := checkpointsState{
		repoDir:   ws.RepoPath,
		wsName:    ws.Name,
		recording: len(m.recording[m.activeID]) > 0,
		vp:        newTranscriptViewport(),
	}
	entries, err := record.List(ws.RepoPath)
	if err != nil {
		cs.listErr = err
	} else {
		cs.entries = entries
	}
	if s := m.selectedSession(); s != nil {
		for i, e := range cs.entries {
			if e.Meta.SessionID == s.ID {
				cs.cursor = i
				break
			}
		}
	}

	m.checkpoints = cs
	m.mode = modeCheckpoints
	m.err = nil
	m.status = ""
	m.sizeCheckpoints()
	m.loadCheckpointDetail()
	return m, nil
}

// updateCheckpoints drives the checkpoints view: the arrow (or vim j/k) keys move
// the list cursor and reload the detail, esc/q returns to the session list, and
// every other key is forwarded to the detail viewport so its paging chords scroll
// the transcript. Closing re-runs afterListChange so the preview/diff panes
// re-target the selection the cockpit returns to.
func (m Model) updateCheckpoints(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "esc", "q":
		m.mode = modeList
		return m, m.afterListChange()
	case "up", "k":
		if m.checkpoints.cursor > 0 {
			m.checkpoints.cursor--
			m.loadCheckpointDetail()
		}
		return m, nil
	case "down", "j":
		if m.checkpoints.cursor < len(m.checkpoints.entries)-1 {
			m.checkpoints.cursor++
			m.loadCheckpointDetail()
		}
		return m, nil
	}
	m.checkpoints.vp, _ = m.checkpoints.vp.Update(msg)
	return m, nil
}

// loadCheckpointDetail reads the selected checkpoint's intent and transcript via
// record.Find — the same read behind `wasa checkpoints show` — and rebuilds the
// detail viewport, scrolling it back to the top for the new selection. A Find
// error is kept on the state so the detail pane can show it without blanking the
// list; a checkpoint with no captured transcript renders a "(not captured)" note.
func (m *Model) loadCheckpointDetail() {
	cs := &m.checkpoints
	cs.intent = ""
	cs.transcript = ""
	cs.captured = false
	cs.loadErr = nil
	if cs.cursor < 0 || cs.cursor >= len(cs.entries) {
		cs.vp.SetContent("")
		return
	}
	e := cs.entries[cs.cursor]
	_, intent, transcript, err := record.Find(cs.repoDir, e.Meta.SessionID)
	if err != nil {
		cs.loadErr = err
		cs.vp.SetContent("")
		return
	}
	cs.intent = strings.TrimSpace(intent)
	if len(transcript) > 0 {
		cs.transcript = record.RenderTranscript(transcript)
		cs.captured = true
	}
	m.refreshCheckpointsContent()
	cs.vp.SetYOffset(0)
}

// refreshCheckpointsContent re-renders the detail body into the viewport at its
// current width. sizeCheckpoints calls it on resize (so a re-wrap follows the new
// width) and loadCheckpointDetail on a selection change. It is a no-op that clears
// the viewport when there is nothing to render (list error, load error, or no
// selection), because the detail pane draws those states itself.
func (m *Model) refreshCheckpointsContent() {
	cs := &m.checkpoints
	if cs.listErr != nil || cs.loadErr != nil ||
		cs.cursor < 0 || cs.cursor >= len(cs.entries) {
		cs.vp.SetContent("")
		return
	}
	cs.vp.SetContent(m.checkpointDetailBody(cs.vp.Width))
}

// sizeCheckpoints sizes the detail viewport to the pane the current layout gives
// it — the right column when the terminal is wide enough, the full width in the
// compact single-column layout — and re-wraps the content to match. It is a
// no-op outside the checkpoints view, so the window-resize handler can call it
// unconditionally.
func (m *Model) sizeCheckpoints() {
	if m.mode != modeCheckpoints {
		return
	}
	var w, h int
	if m.compactLayout() {
		w = max(m.width-4, 1)
		h = max(m.height-6, 1)
	} else {
		bodyH := max(m.height-chromeRows, 3)
		detailW := m.width - m.listColWidth() - 4
		w = max(detailW-2, 1)
		h = max(bodyH-2, 1)
	}
	m.checkpoints.vp.Width = w
	m.checkpoints.vp.Height = h
	m.refreshCheckpointsContent()
}

// compactLayout reports whether the terminal is too small for the two-column
// layout, mirroring listView's threshold check.
func (m Model) compactLayout() bool {
	return m.width < m.cfg.Layout.CompactWidth ||
		m.height < m.cfg.Layout.CompactHeight
}

// checkpointsView renders the checkpoints browser: a header, the list column and
// the scrollable detail column, the view's key hints and the shared status line —
// the same frame shape as listView so it reads as the same app, not a new one.
func (m Model) checkpointsView() string {
	if m.compactLayout() {
		return m.checkpointsCompactView()
	}
	bodyH := max(m.height-chromeRows, 3)
	listW := m.listColWidth()
	detailW := m.width - listW - 4

	list := m.theme.PaneStyle.Width(listW).Height(bodyH).Render(
		m.paneTitle("checkpoints") + m.checkpointsCountBadge() + "\n" +
			m.checkpointsListBody(listW),
	)
	detail := m.theme.PaneStyle.Width(detailW).Height(bodyH).Render(
		m.checkpointsDetailPane(detailW - 2),
	)
	body := lipgloss.JoinHorizontal(lipgloss.Top, list, detail)

	return lipgloss.JoinVertical(
		lipgloss.Left,
		m.checkpointsHeader(),
		body,
		m.checkpointsMenuBar(),
		m.statusLine(),
	)
}

// checkpointsCompactView is the single-column fallback for a terminal too narrow
// for the two panes: the detail fills the width and the list is dropped, its
// position shown in the header, so browsing still works without overflowing.
func (m Model) checkpointsCompactView() string {
	w := max(m.width-2, 1)
	detail := m.theme.PaneStyle.Width(w).Render(
		m.checkpointsDetailPane(w - 2),
	)
	parts := []string{
		m.checkpointsHeader(),
		detail,
		m.checkpointsMenuBar(),
	}
	if s := m.statusLine(); s != "" {
		parts = append(parts, s)
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// checkpointsHeader is the view's title line: the workspace it is showing and,
// when a checkpoint is selected, its position in the list.
func (m Model) checkpointsHeader() string {
	label := "checkpoints"
	if m.checkpoints.wsName != "" {
		label += " · " + m.checkpoints.wsName
	}
	if n := len(m.checkpoints.entries); n > 0 {
		label += fmt.Sprintf(" · %d/%d", m.checkpoints.cursor+1, n)
	}
	return m.theme.ActiveTabStyle.Render(label)
}

// checkpointsCountBadge shows the number of listed checkpoints beside the list
// title, empty when there are none.
func (m Model) checkpointsCountBadge() string {
	n := len(m.checkpoints.entries)
	if n == 0 {
		return ""
	}
	return m.theme.DimStyle.Render(fmt.Sprintf("  (%d)", n))
}

// checkpointsListBody renders the checkpoint list: one two-line row per recorded
// session in record.List order (newest first), or the appropriate empty/error
// state when there is nothing to list.
func (m Model) checkpointsListBody(paneW int) string {
	cs := m.checkpoints
	inner := paneW - 2
	if cs.listErr != nil {
		return m.theme.ErrorStyle.Render(
			component.PadAnsi("  "+cs.listErr.Error(), inner),
		)
	}
	if len(cs.entries) == 0 {
		return m.theme.DimStyle.Render(m.checkpointsEmptyMessage())
	}
	var b strings.Builder
	for i, e := range cs.entries {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(m.checkpointRow(i, e, inner))
		b.WriteString("\n")
	}
	return b.String()
}

// checkpointRow renders one checkpoint as a two-line row mirroring the session
// list: a title line with the record glyph and session id, and a dim detail line
// with the branch, when and commit count. The selected row takes the selection
// band styles.
func (m Model) checkpointRow(i int, e record.Entry, w int) string {
	titleS, descS := m.theme.RowTitleStyle, m.theme.RowDescStyle
	if i == m.checkpoints.cursor {
		titleS, descS = m.theme.SelRowTitleStyle, m.theme.SelRowDescStyle
	}
	head := fmt.Sprintf(" %s %s", recordIcon, e.Meta.SessionID)
	branch := e.Meta.Branch
	if branch == "" {
		branch = "—"
	}
	sub := fmt.Sprintf("   %s %s · %s · %d commits",
		branchIcon, branch, e.When.Local().Format("01-02 15:04"),
		len(e.Meta.Commits),
	)
	return lipgloss.JoinVertical(
		lipgloss.Left,
		titleS.Render(component.PadAnsi(head, w)),
		descS.Render(component.PadAnsi(sub, w)),
	)
}

// checkpointsDetailPane renders the right column: the selected checkpoint's
// intent, meta and transcript in the scrollable viewport, or an error/empty state
// when there is nothing to show. w is the pane's inner width.
func (m Model) checkpointsDetailPane(w int) string {
	cs := m.checkpoints
	title := m.paneTitle("detail")
	switch {
	case cs.listErr != nil:
		return title + "\n" + m.theme.ErrorStyle.Render(
			wrapTo("could not list checkpoints: "+cs.listErr.Error(), w),
		)
	case len(cs.entries) == 0:
		return title + "\n" + m.theme.DimStyle.Render(
			wrapTo(m.checkpointsEmptyMessage(), w),
		)
	case cs.loadErr != nil:
		return title + "\n" + m.theme.ErrorStyle.Render(
			wrapTo("could not load checkpoint: "+cs.loadErr.Error(), w),
		)
	}
	return lipgloss.JoinVertical(lipgloss.Left, title, cs.vp.View())
}

// checkpointDetailBody builds the scrollable detail: the intent, a compact meta
// block and the rendered transcript, each under its own heading, wrapped to width
// so nothing overflows the pane. The transcript is record.RenderTranscript's
// output — byte-for-byte what `wasa checkpoints show` renders.
func (m Model) checkpointDetailBody(width int) string {
	cs := m.checkpoints
	e := cs.entries[cs.cursor]
	var b strings.Builder

	b.WriteString(m.theme.PaneTitleStyle.Render("intent"))
	b.WriteByte('\n')
	if cs.intent == "" {
		b.WriteString(m.theme.DimStyle.Render("  (none)"))
	} else {
		b.WriteString(wrapTo(cs.intent, width))
	}
	b.WriteString("\n\n")

	b.WriteString(m.theme.PaneTitleStyle.Render("meta"))
	b.WriteByte('\n')
	b.WriteString(m.checkpointMeta(e))
	b.WriteString("\n\n")

	b.WriteString(m.theme.PaneTitleStyle.Render("transcript"))
	b.WriteByte('\n')
	if !cs.captured {
		b.WriteString(m.theme.DimStyle.Render("  (not captured)"))
	} else {
		b.WriteString(wrapTo(cs.transcript, width))
	}
	return b.String()
}

// checkpointMeta renders the compact meta block: the fields worth reading at a
// glance, mirroring the `wasa checkpoints` columns plus the agent, with labels in
// the dim style.
func (m Model) checkpointMeta(e record.Entry) string {
	rows := [][2]string{
		{"session", e.Meta.SessionID},
		{"branch", orDash(e.Meta.Branch)},
		{"agent", orDash(e.Meta.Agent)},
		{"when", e.When.Local().Format("2006-01-02 15:04")},
		{"commits", fmt.Sprintf("%d", len(e.Meta.Commits))},
		{"state", checkpointStateLabel(e)},
	}
	var b strings.Builder
	for i, r := range rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(m.theme.DimStyle.Render(component.Pad("  "+r[0], 11)))
		b.WriteString(r[1])
	}
	return b.String()
}

// checkpointsEmptyMessage is the message shown when the repo lists no checkpoints:
// with recording on it is simply empty; with recording off it points at the
// toggle (a repo with no checkpoints ref reads as off, which is the right hint).
func (m Model) checkpointsEmptyMessage() string {
	if m.checkpoints.recording {
		return "  no checkpoints recorded yet"
	}
	return "  recording is off for this workspace\n" +
		"  press " + m.menuKey(config.ActionRecordToggle) +
		" to start recording checkpoints"
}

// checkpointsMenuBar is the view's key hints, in the shared menu-bar styling, so
// the new keys are documented on screen the way every other mode's are.
func (m Model) checkpointsMenuBar() string {
	items := [][2]string{
		{"↑↓", "select"},
		{"⇞⇟", "scroll"},
		{"esc", "back"},
	}
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = m.theme.MenuKeyStyle.Render(it[0]) + " " +
			m.theme.MenuDescStyle.Render(it[1])
	}
	return " " + strings.Join(parts, m.theme.MenuSepStyle.Render(menuSep))
}

// checkpointStateLabel derives a checkpoint's state the way the `wasa checkpoints`
// STATE column does: open until finished, annotated imported or unmanaged.
func checkpointStateLabel(e record.Entry) string {
	state := "open"
	if !e.Meta.FinishedAt.IsZero() {
		state = "finished"
	}
	switch {
	case e.Meta.Imported:
		state += ", imported"
	case e.Meta.Unmanaged:
		state += ", unmanaged"
	}
	return state
}

// orDash returns s, or an em dash when s is empty, so a missing field reads as
// deliberately absent rather than blank.
func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// wrapTo hard-wraps each line of s to w cells so long transcript or intent lines
// wrap within the viewport rather than overflowing the pane. It wraps per line to
// preserve the transcript's existing line breaks.
func wrapTo(s string, w int) string {
	if w <= 0 {
		return s
	}
	var b strings.Builder
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(ansi.Wrap(line, w, ""))
	}
	return b.String()
}
