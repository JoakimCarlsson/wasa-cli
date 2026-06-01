package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/joakimcarlsson/wasa/internal/registry"
	"github.com/joakimcarlsson/wasa/internal/worktree"
)

// diffMsg carries the computed diff of a worktree session against its base
// commit. sessionID tags the session it was computed for, so a diff that
// arrives after the selection moved is dropped rather than shown under the wrong
// session. An empty text with no err is a clean worktree (no changes).
type diffMsg struct {
	sessionID string
	text      string
	added     int
	removed   int
	err       error
}

// diffPane is the on-demand diff feature machine: it owns the scrollable
// viewport and the computed diff of the selected worktree session against its
// recorded base commit. sid tags the session the loaded diff belongs to, so the
// diff is computed once per selection and a stale delivery is dropped.
type diffPane struct {
	vp      viewport.Model
	sid     string
	text    string
	added   int
	removed int
	err     error
}

// newDiffPane builds the Diff tab's scrollable viewport with a keymap that
// avoids the cockpit list bindings: it scrolls with PageUp/PageDown and the
// ctrl+f/ctrl+b/ctrl+u/ctrl+d chords and leaves the bare arrow keys to the list
// so up/down keep moving the session cursor (which re-targets the diff).
func newDiffPane() diffPane {
	vp := viewport.New(0, 0)
	vp.KeyMap = viewport.KeyMap{
		PageDown:     key.NewBinding(key.WithKeys("pgdown", "ctrl+f")),
		PageUp:       key.NewBinding(key.WithKeys("pgup", "ctrl+b")),
		HalfPageDown: key.NewBinding(key.WithKeys("ctrl+d")),
		HalfPageUp:   key.NewBinding(key.WithKeys("ctrl+u")),
	}
	return diffPane{vp: vp}
}

// setSize sizes the diff viewport to the pane body minus the summary line, so
// its paging math and render match what view draws. It runs on resize and
// whenever a diff is loaded, never per tick.
func (d *diffPane) setSize(w, h int) {
	d.vp.Width = max(w, 1)
	d.vp.Height = max(h-1, 1)
}

// handleKey forwards a key to the viewport so the diff scrolls; it is called
// only while the Diff tab is active.
func (d *diffPane) handleKey(msg tea.Msg) {
	d.vp, _ = d.vp.Update(msg)
}

// ensure returns a command that computes s's diff against its recorded base
// commit, when that diff is not already loaded. It is a no-op (nil) when the
// diff for the selection is already shown, so it fires only on a selection
// change or a switch to the Diff tab, never per tick. A plain (non-worktree)
// session loads an empty diff; view renders the explanatory state for it from
// the session's own fields.
func (d *diffPane) ensure(s *registry.Session, reg *registry.Registry, home string) tea.Cmd {
	if s == nil || d.sid == s.ID {
		return nil
	}
	sid := s.ID
	branch, wt, base := s.Branch, s.WorktreePath, s.BaseCommit
	if branch == "" || wt == "" || base == "" {
		return func() tea.Msg { return diffMsg{sessionID: sid} }
	}
	ws, ok := reg.Workspace(s.WorkspaceID)
	if !ok {
		return func() tea.Msg {
			return diffMsg{
				sessionID: sid,
				err:       fmt.Errorf("workspace not found"),
			}
		}
	}
	repo, wsID := ws.RepoPath, s.WorkspaceID
	return func() tea.Msg {
		res, err := worktree.New(repo, home, wsID).Diff(wt, base)
		if err != nil {
			return diffMsg{sessionID: sid, err: err}
		}
		return diffMsg{
			sessionID: sid, text: res.Text,
			added: res.Added, removed: res.Removed,
		}
	}
}

// apply stores a computed diff for rendering, dropping a delivery whose session
// is no longer selected so a slow diff cannot overwrite the body after the
// cursor moved. It loads the colorized content into the viewport and scrolls it
// back to the top for the new session.
func (d *diffPane) apply(th Theme, msg diffMsg, s *registry.Session) {
	if s == nil || s.ID != msg.sessionID {
		return
	}
	d.sid = msg.sessionID
	d.err = msg.err
	d.text = msg.text
	d.added, d.removed = msg.added, msg.removed
	d.vp.SetContent(colorizeDiff(th, msg.text))
	d.vp.SetYOffset(0)
}

// view renders the Diff tab: a colorized git diff of the selected worktree
// session against its recorded base commit, in a scrollable viewport under an
// additions/deletions summary line. A plain (non-worktree) session shows an
// explanatory state rather than an error; a worktree with no changes shows an
// empty state; and the diff is shown only once it has been computed for the
// current selection.
func (d diffPane) view(th Theme, s *registry.Session, w, h int) string {
	if s == nil {
		return th.dimStyle.Render("No session selected.")
	}
	if s.Branch == "" || s.WorktreePath == "" || s.BaseCommit == "" {
		return th.dimStyle.Render(
			"Diff is only available for worktree sessions.",
		)
	}
	if d.sid != s.ID {
		return th.dimStyle.Render("Loading diff…")
	}
	if d.err != nil {
		return th.errorStyle.Render("diff error: " + d.err.Error())
	}
	if strings.TrimSpace(d.text) == "" {
		return th.dimStyle.Render("No changes.")
	}

	vp := d.vp
	vp.Width = max(w, 1)
	vp.Height = max(h-1, 1)
	return diffSummaryLine(th, d.added, d.removed) + "\n" + vp.View()
}

// diffSummaryLine renders the "N additions(+) / M deletions(-)" header above the
// diff, the additions in the add colour and the deletions in the delete colour.
func diffSummaryLine(th Theme, added, removed int) string {
	return th.diffAddStyle.Render(fmt.Sprintf("%d additions(+)", added)) +
		th.dimStyle.Render(" / ") +
		th.diffDelStyle.Render(fmt.Sprintf("%d deletions(-)", removed))
}

// colorizeDiff styles a plain unified diff line by line: hunk headers in the
// accent, additions green and deletions red, and the file/metadata lines dimmed.
// The context lines are left unstyled. git emits the diff without colour, so the
// cockpit colours it itself to match the theme.
func colorizeDiff(th Theme, text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = styleDiffLine(th, line)
	}
	return strings.Join(lines, "\n")
}

func styleDiffLine(th Theme, line string) string {
	switch {
	case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
		return th.diffMetaStyle.Render(line)
	case strings.HasPrefix(line, "@@"):
		return th.diffHunkStyle.Render(line)
	case strings.HasPrefix(line, "+"):
		return th.diffAddStyle.Render(line)
	case strings.HasPrefix(line, "-"):
		return th.diffDelStyle.Render(line)
	case strings.HasPrefix(line, "diff "),
		strings.HasPrefix(line, "index "),
		strings.HasPrefix(line, "new file"),
		strings.HasPrefix(line, "deleted file"),
		strings.HasPrefix(line, "rename "),
		strings.HasPrefix(line, "similarity "):
		return th.diffMetaStyle.Render(line)
	default:
		return line
	}
}
