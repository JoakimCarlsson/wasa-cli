package pane

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/joakimcarlsson/wasa/internal/tui/theme"
	"github.com/joakimcarlsson/wasa/internal/worktree"
)

// DiffMsg carries the computed diff of a worktree session against its base
// commit. sessionID tags the session it was computed for, so a diff that
// arrives after the selection moved is dropped rather than shown under the
// wrong session. An empty text with no err is a clean worktree (no changes).
type DiffMsg struct {
	sessionID string
	text      string
	added     int
	removed   int
	err       error
}

// SessionID is the session the diff was computed for. The root reads it to drop
// a delivery whose session is no longer selected.
func (m DiffMsg) SessionID() string {
	return m.sessionID
}

// NewDiffErr builds a DiffMsg carrying err for sessionID. The root uses it to
// surface a workspace it could not resolve as the diff's error state.
func NewDiffErr(sessionID string, err error) DiffMsg {
	return DiffMsg{sessionID: sessionID, err: err}
}

// DiffSession is the minimal set of facts the Diff body needs about the
// selected session to choose its render state without the pane reaching into
// the registry: whether one is selected, whether it is a worktree session, and
// its id so a not-yet-loaded selection shows the loading state.
type DiffSession struct {
	Selected     bool
	ID           string
	Branch       string
	WorktreePath string
	BaseCommit   string
}

// Diff owns the Diff tab's scrollable viewport and the last computed diff: the
// session it was computed for, its text and add/remove counts, and any error.
// theme is the resolved theme the colorizer uses; the root refreshes it via
// SetTheme when the config editor changes it.
type Diff struct {
	theme   theme.Theme
	vp      viewport.Model
	sid     string
	text    string
	added   int
	removed int
	err     error
}

// NewDiff builds a Diff with its viewport configured against theme.
func NewDiff(theme theme.Theme) Diff {
	return Diff{theme: theme, vp: newDiffViewport()}
}

// SetTheme refreshes the theme the colorizer renders with. The root calls it
// when the in-cockpit config editor changes the theme.
func (d *Diff) SetTheme(theme theme.Theme) {
	d.theme = theme
}

// newDiffViewport builds the Diff tab's scrollable viewport with a keymap that
// avoids the cockpit list bindings: it scrolls with PageUp/PageDown and the
// ctrl+f/ctrl+b/ctrl+u/ctrl+d chords and leaves the bare arrow keys to the list
// so up/down keep moving the session cursor (which re-targets the diff).
func newDiffViewport() viewport.Model {
	vp := viewport.New(0, 0)
	vp.KeyMap = viewport.KeyMap{
		PageDown:     key.NewBinding(key.WithKeys("pgdown", "ctrl+f")),
		PageUp:       key.NewBinding(key.WithKeys("pgup", "ctrl+b")),
		HalfPageDown: key.NewBinding(key.WithKeys("ctrl+d")),
		HalfPageUp:   key.NewBinding(key.WithKeys("ctrl+u")),
	}
	return vp
}

// Update forwards a message to the diff viewport so it scrolls. The root calls
// it on the Diff tab in list mode.
func (d *Diff) Update(msg tea.Msg) {
	d.vp, _ = d.vp.Update(msg)
}

// EnsureCmd returns a command that computes the selected worktree session's
// diff against baseCommit, when that diff is not already loaded for sessionID.
// It is a no-op (nil) when the diff for the selection is already shown, so it
// fires only on a selection change or a switch to the Diff tab, never per tick.
// A plain (non-worktree) session — empty branch, worktree or base — loads an
// empty diff; Body renders the explanatory state for it. The root computes
// repoPath, home and workspaceID from the selected session and its workspace
// and passes them in, so the pane runs only the worktree diff itself.
func (d *Diff) EnsureCmd(
	sessionID, repoPath, home, workspaceID, worktreePath, baseCommit string,
) tea.Cmd {
	if sessionID == "" || d.sid == sessionID {
		return nil
	}
	sid := sessionID
	if baseCommit == "" || worktreePath == "" {
		return func() tea.Msg { return DiffMsg{sessionID: sid} }
	}
	repo, wt, base, wsID := repoPath, worktreePath, baseCommit, workspaceID
	return func() tea.Msg {
		res, err := worktree.New(repo, home, wsID).Diff(wt, base)
		if err != nil {
			return DiffMsg{sessionID: sid, err: err}
		}
		return DiffMsg{
			sessionID: sid, text: res.Text,
			added: res.Added, removed: res.Removed,
		}
	}
}

// Apply stores a computed diff for rendering. The root has already dropped a
// delivery whose session is no longer selected. It loads the colorized content
// into the viewport and scrolls it back to the top for the new session. The
// root sizes the viewport (Size) before applying so the paging math matches the
// current pane.
func (d *Diff) Apply(msg DiffMsg) tea.Cmd {
	d.sid = msg.sessionID
	d.err = msg.err
	d.text = msg.text
	d.added, d.removed = msg.added, msg.removed
	d.vp.SetContent(colorizeDiff(d.theme, msg.text))
	d.vp.SetYOffset(0)
	return nil
}

// SID is the session id the loaded diff was computed for. The root reads it to
// drop a delivery whose session is no longer selected.
func (d Diff) SID() string {
	return d.sid
}

// Size sizes the diff viewport to the pane body minus the summary line, so its
// paging math and render match what Body draws. The root runs it on resize and
// whenever a diff is loaded, never per tick.
func (d *Diff) Size(w, h int) {
	d.vp.Width = max(w, 1)
	d.vp.Height = max(h-1, 1)
}

// Body renders the Diff tab: a colorized git diff of the selected worktree
// session against its recorded base commit, in a scrollable viewport under an
// additions/deletions summary line. With no session selected it says so; a
// plain (non-worktree) session shows an explanatory state rather than an error;
// a worktree whose diff is not yet computed shows a loading state; and a clean
// worktree shows an empty state.
func (d Diff) Body(t theme.Theme, sess DiffSession, w, h int) string {
	if !sess.Selected {
		return t.DimStyle.Render("No session selected.")
	}
	if sess.Branch == "" || sess.WorktreePath == "" || sess.BaseCommit == "" {
		return t.DimStyle.Render(
			"Diff is only available for worktree sessions.",
		)
	}
	if d.sid != sess.ID {
		return t.DimStyle.Render("Loading diff…")
	}
	if d.err != nil {
		return t.ErrorStyle.Render("diff error: " + d.err.Error())
	}
	if strings.TrimSpace(d.text) == "" {
		return t.DimStyle.Render("No changes.")
	}

	vp := d.vp
	vp.Width = max(w, 1)
	vp.Height = max(h-1, 1)
	return diffSummaryLine(t, d.added, d.removed) + "\n" + vp.View()
}

// diffSummaryLine renders the "N additions(+) / M deletions(-)" header above
// the diff, the additions in the add colour and the deletions in the delete
// colour.
func diffSummaryLine(theme theme.Theme, added, removed int) string {
	return theme.DiffAddStyle.Render(fmt.Sprintf("%d additions(+)", added)) +
		theme.DimStyle.Render(" / ") +
		theme.DiffDelStyle.Render(fmt.Sprintf("%d deletions(-)", removed))
}

// colorizeDiff styles a plain unified diff line by line: hunk headers in the
// accent, additions green and deletions red, and the file/metadata lines
// dimmed. The context lines are left unstyled. git emits the diff without
// colour, so the cockpit colours it itself to match the theme.
func colorizeDiff(theme theme.Theme, text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = styleDiffLine(theme, line)
	}
	return strings.Join(lines, "\n")
}

func styleDiffLine(theme theme.Theme, line string) string {
	switch {
	case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
		return theme.DiffMetaStyle.Render(line)
	case strings.HasPrefix(line, "@@"):
		return theme.DiffHunkStyle.Render(line)
	case strings.HasPrefix(line, "+"):
		return theme.DiffAddStyle.Render(line)
	case strings.HasPrefix(line, "-"):
		return theme.DiffDelStyle.Render(line)
	case strings.HasPrefix(line, "diff "),
		strings.HasPrefix(line, "index "),
		strings.HasPrefix(line, "new file"),
		strings.HasPrefix(line, "deleted file"),
		strings.HasPrefix(line, "rename "),
		strings.HasPrefix(line, "similarity "):
		return theme.DiffMetaStyle.Render(line)
	default:
		return line
	}
}
