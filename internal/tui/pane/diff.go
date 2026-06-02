package pane

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/joakimcarlsson/wasa/internal/tui/component"
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
	return diffCmd(
		sessionID,
		repoPath,
		home,
		workspaceID,
		worktreePath,
		baseCommit,
	)
}

// RefreshCmd recomputes the diff for the already-loaded selection, bypassing the
// EnsureCmd no-op guard so the live-refresh tick can pick up changes an agent
// makes in the worktree without the user re-selecting the row. It is for
// worktree sessions only: a plain session — empty base or worktree — returns nil
// rather than reloading the empty state every tick. Apply keeps the scroll
// position across a refresh, so the pane updates in place.
func (d *Diff) RefreshCmd(
	sessionID, repoPath, home, workspaceID, worktreePath, baseCommit string,
) tea.Cmd {
	if sessionID == "" || baseCommit == "" || worktreePath == "" {
		return nil
	}
	return diffCmd(
		sessionID,
		repoPath,
		home,
		workspaceID,
		worktreePath,
		baseCommit,
	)
}

// diffCmd builds the command that computes a session's diff and tags the result
// with sessionID. A plain (non-worktree) session — empty base or worktree —
// yields an empty DiffMsg so Body renders its explanatory state; a worktree
// session runs the full diff against baseCommit. It is the shared body of
// EnsureCmd (selection change) and RefreshCmd (live tick).
func diffCmd(
	sessionID, repoPath, home, workspaceID, worktreePath, baseCommit string,
) tea.Cmd {
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
// into the viewport, scrolling back to the top only when the diff is for a
// different session than the one shown — a live refresh of the same session
// keeps the scroll position so the pane updates in place rather than jumping to
// the top each tick. The root sizes the viewport (Size) before applying so the
// paging math matches the current pane.
func (d *Diff) Apply(msg DiffMsg) tea.Cmd {
	changed := d.sid != msg.sessionID
	d.sid = msg.sessionID
	d.err = msg.err
	d.text = msg.text
	d.added, d.removed = msg.added, msg.removed
	d.vp.SetContent(renderDiff(d.theme, msg.text, d.vp.Width))
	if changed {
		d.vp.SetYOffset(0)
	}
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

// gutterDigits is the width of each line-number column in the diff gutter, and
// gutterWidth is the full gutter the line-number columns and their separators
// occupy before a line's content: "<old> <new> ".
const (
	gutterDigits = 4
	gutterWidth  = gutterDigits*2 + 2
)

// tab is what a literal tab expands to in rendered diff content, so columns line
// up under a fixed-width gutter rather than jumping at the terminal's tab stops.
const tab = "    "

// renderDiff turns a plain unified git diff into the readable pane body, in the
// spirit of git-delta: each file gets a styled header bar (the diff/index/mode
// and ---/+++ noise lines are dropped), each hunk a dim rule carrying its
// section context, every line an old/new number gutter, and added and removed
// lines a full-width colour band so changes scan as blocks rather than as lone
// tinted characters. git emits the diff uncoloured, so the cockpit lays this out
// itself to match the theme. width is the viewport width the bands fill to.
func renderDiff(t theme.Theme, text string, width int) string {
	width = max(width, 1)
	contentW := max(width-gutterWidth, 1)

	var (
		b            strings.Builder
		oldLn, newLn int
		firstFile    = true
	)
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git"):
			if !firstFile {
				b.WriteByte('\n')
			}
			firstFile = false
			b.WriteString(t.DiffFileStyle.Render(
				component.Pad("▌ "+diffPath(line), width),
			))
			b.WriteByte('\n')
		case isDiffNoise(line):
			continue
		case strings.HasPrefix(line, "@@"):
			oldLn, newLn = parseHunk(line)
			b.WriteString(hunkRule(t, line, width))
			b.WriteByte('\n')
		case strings.HasPrefix(line, "+"):
			b.WriteString(diffLine(
				t.DiffAddLineStyle, t.DiffAddStyle.GetForeground(),
				t, "", newLn, "+", line[1:], contentW,
			))
			b.WriteByte('\n')
			newLn++
		case strings.HasPrefix(line, "-"):
			b.WriteString(diffLine(
				t.DiffDelLineStyle, t.DiffDelStyle.GetForeground(),
				t, itoa(oldLn), 0, "-", line[1:], contentW,
			))
			b.WriteByte('\n')
			oldLn++
		case strings.HasPrefix(line, "\\"):
			b.WriteString(t.DiffMetaStyle.Render("  " + line))
			b.WriteByte('\n')
		default:
			content := line
			if strings.HasPrefix(line, " ") {
				content = line[1:]
			}
			b.WriteString(contextLine(t, oldLn, newLn, content, contentW))
			b.WriteByte('\n')
			oldLn++
			newLn++
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// diffLine renders one added or removed line: the gutter (only the relevant
// side numbered) followed by the sign and content on a full-width colour band,
// with the sign in the strong add/remove colour over the same band.
func diffLine(
	band lipgloss.Style,
	signColor lipgloss.TerminalColor,
	t theme.Theme,
	oldStr string, newLn int,
	sign, content string,
	contentW int,
) string {
	newStr := ""
	if newLn > 0 {
		newStr = itoa(newLn)
	}
	body := component.Pad(sign+expandTabs(content), contentW)
	signed := band.Foreground(signColor).Bold(true).Render(sign) +
		band.Render(strings.TrimPrefix(body, sign))
	return gutter(t, oldStr, newStr) + signed
}

// contextLine renders an unchanged line: both line numbers in the dim gutter and
// the content aligned under the sign column in the default colour, with no band,
// so the surrounding code stays legible while the changed lines carry the tint.
func contextLine(
	t theme.Theme,
	oldLn, newLn int,
	content string,
	contentW int,
) string {
	return gutter(t, itoa(oldLn), itoa(newLn)) +
		component.Pad(" "+expandTabs(content), contentW)
}

// gutter renders the old/new line-number columns, dimmed; an empty string leaves
// that side blank (an added line has no old number, a removed line no new one).
func gutter(t theme.Theme, oldStr, newStr string) string {
	return t.DiffGutterStyle.Render(fmt.Sprintf(
		"%*s %*s ", gutterDigits, oldStr, gutterDigits, newStr,
	))
}

// hunkRule renders a hunk as a dim horizontal rule carrying the section context
// git puts after the second "@@" (often the enclosing function), so hunks read
// as labelled separators rather than raw @@ coordinates.
func hunkRule(t theme.Theme, line string, width int) string {
	head := "──"
	if section := hunkSection(line); section != "" {
		head = "── " + section + " "
	}
	if pad := width - ansi.StringWidth(head); pad > 0 {
		head += strings.Repeat("─", pad)
	}
	return t.DiffMetaStyle.Render(head)
}

// isDiffNoise reports whether line is a unified-diff metadata line the pane hides
// because the file header bar already conveys what changed: the index/mode lines
// and the ---/+++ path lines.
func isDiffNoise(line string) bool {
	for _, p := range []string{
		"index ", "old mode ", "new mode ", "new file mode ",
		"deleted file mode ", "similarity ", "dissimilarity ",
		"rename ", "copy ", "--- ", "+++ ",
	} {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

// diffPath extracts the file a "diff --git a/<old> b/<new>" line concerns,
// preferring the new (b/) path; it falls back to the whole line tail when the
// expected shape is absent.
func diffPath(line string) string {
	if i := strings.LastIndex(line, " b/"); i >= 0 {
		return line[i+3:]
	}
	return strings.TrimPrefix(line, "diff --git ")
}

// parseHunk reads the old and new starting line numbers from a hunk header
// "@@ -oldStart,oldLines +newStart,newLines @@".
func parseHunk(line string) (oldStart, newStart int) {
	for _, f := range strings.Fields(line) {
		switch {
		case strings.HasPrefix(f, "-"):
			oldStart = atoiBeforeComma(f[1:])
		case strings.HasPrefix(f, "+"):
			newStart = atoiBeforeComma(f[1:])
		}
	}
	return oldStart, newStart
}

// hunkSection returns the context git appends after the second "@@" of a hunk
// header, or "" when there is none.
func hunkSection(line string) string {
	rest := strings.TrimPrefix(line, "@@")
	if i := strings.Index(rest, "@@"); i >= 0 {
		return strings.TrimSpace(rest[i+2:])
	}
	return ""
}

func atoiBeforeComma(s string) int {
	if i := strings.IndexByte(s, ','); i >= 0 {
		s = s[:i]
	}
	n, _ := strconv.Atoi(s)
	return n
}

func itoa(n int) string { return strconv.Itoa(n) }

func expandTabs(s string) string { return strings.ReplaceAll(s, "\t", tab) }
