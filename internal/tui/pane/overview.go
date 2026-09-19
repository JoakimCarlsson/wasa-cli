package pane

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/joakimcarlsson/wasa-cli/internal/tui/component"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/layout"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/theme"
)

// OverviewSession is everything the Overview tab draws, projected out of the
// registry by the root so the pane never reaches into it. Every field is a fact
// wasa itself owns — what it launched, where, how long ago, what the work has
// produced — which is the point of the tab: it is drawn from wasa's own model
// rather than scraped off another program's screen.
type OverviewSession struct {
	Selected bool

	Title       string
	Agent       string
	Profile     string
	Status      string
	StatusStyle lipgloss.Style
	StatusIcon  string

	Branch       string
	WorktreePath string
	WorkingDir   string
	BaseCommit   string
	Backend      string

	Started  time.Time
	ExitCode *int

	Added   int
	Removed int
	Churned bool

	Recorded    bool
	Commits     int
	LastRecord  time.Time
	Recording   []string
	Collisions  []string
	ResumedFrom string
}

// Overview renders the right pane's first tab. It holds no state: everything it
// draws arrives in the projection, so the root can hand it a fresh one each
// frame without the pane owning a lifecycle.
type Overview struct{}

// NewOverview builds the Overview tab.
func NewOverview() Overview { return Overview{} }

// Body renders the tab: a heading with the session's title and live status,
// then the facts in labelled groups — where the work is happening, what it has
// produced, and what has been recorded of it — each group under a faint rule.
func (o Overview) Body(t theme.Theme, s OverviewSession, w, h int) string {
	if !s.Selected {
		return t.DimStyle.Render("No session selected.")
	}

	blocks := []string{
		o.heading(t, s, w),
		o.group(t, w, "where", o.whereRows(s)),
		o.group(t, w, "work", o.workRows(t, s)),
		o.group(t, w, "record", o.recordRows(t, s)),
	}
	if warn := o.warning(t, s, w); warn != "" {
		blocks = append(blocks, warn)
	}
	body := strings.Join(blocks, strings.Repeat("\n", layout.Snug))
	return component.Clamp(body, h)
}

// heading is the session's title with its status glyph and label beside it,
// over the agent and profile that produced it.
func (o Overview) heading(t theme.Theme, s OverviewSession, w int) string {
	status := s.StatusStyle.Render(s.StatusIcon + " " + s.Status)
	title := t.TitleStyle.Render(s.Title)
	gap := max(w-ansi.StringWidth(title)-ansi.StringWidth(status), 1)
	head := title + strings.Repeat(" ", gap) + status

	sub := s.Agent
	if s.Profile != "" {
		sub += " · " + s.Profile
	}
	if !s.Started.IsZero() {
		sub += " · up " + humanSince(s.Started)
	}
	return head + "\n" + t.DimStyle.Render(sub)
}

// whereRows are the facts about where the session is running.
func (o Overview) whereRows(s OverviewSession) [][2]string {
	dir := s.WorktreePath
	kind := "worktree"
	if dir == "" {
		dir, kind = s.WorkingDir, "directory"
	}
	rows := [][2]string{{kind, orEmDash(dir)}}
	if s.Branch != "" {
		rows = append(rows, [2]string{"branch", s.Branch})
	}
	if s.BaseCommit != "" {
		rows = append(rows, [2]string{"base", shortSHA(s.BaseCommit)})
	}
	if s.Backend != "" {
		rows = append(rows, [2]string{"backend", s.Backend})
	}
	return rows
}

// workRows are the facts about what the session has changed. A worktree whose
// churn has not been computed says so rather than claiming zero.
func (o Overview) workRows(t theme.Theme, s OverviewSession) [][2]string {
	if s.Branch == "" || s.WorktreePath == "" {
		return [][2]string{{"changes", "—  plain session, no worktree"}}
	}
	churn := "no changes yet"
	if s.Churned {
		churn = t.DiffAddStyle.Render(fmt.Sprintf("+%d", s.Added)) + " / " +
			t.DiffDelStyle.Render(fmt.Sprintf("−%d", s.Removed))
	}
	rows := [][2]string{{"changes", churn}}
	if s.ResumedFrom != "" {
		rows = append(rows, [2]string{"resumed", s.ResumedFrom})
	}
	if s.ExitCode != nil {
		rows = append(rows, [2]string{"exit", fmt.Sprintf("%d", *s.ExitCode)})
	}
	return rows
}

// recordRows are the facts about what has been recorded of the session.
func (o Overview) recordRows(t theme.Theme, s OverviewSession) [][2]string {
	state := t.DimStyle.Render("off for this repo")
	if len(s.Recording) > 0 {
		state = t.DiffAddStyle.Render("on") +
			t.DimStyle.Render(" · "+strings.Join(s.Recording, ", "))
	}
	rows := [][2]string{{"recording", state}}
	if !s.Recorded {
		return append(rows, [2]string{"checkpoint", "none yet"})
	}
	rows = append(rows, [2]string{
		"checkpoint", fmt.Sprintf("%d commits", s.Commits),
	})
	if !s.LastRecord.IsZero() {
		rows = append(
			rows,
			[2]string{"last", humanSince(s.LastRecord) + " ago"},
		)
	}
	return rows
}

// warning renders the collision notice under its own rule, so an overlap with
// another session is the one thing on the tab that carries the danger colour.
func (o Overview) warning(t theme.Theme, s OverviewSession, w int) string {
	if len(s.Collisions) == 0 {
		return ""
	}
	return sectionRule(t, "conflicts", w) + "\n" +
		t.ErrorStyle.Render(
			indent()+"also edited by "+strings.Join(s.Collisions, ", "),
		)
}

// group renders a labelled block: the heading with its rule, then one line per
// row with the labels aligned into a column.
func (o Overview) group(
	t theme.Theme, w int, title string, rows [][2]string,
) string {
	if len(rows) == 0 {
		return ""
	}
	labelW := 0
	for _, r := range rows {
		labelW = max(labelW, len(r[0]))
	}
	var b strings.Builder
	b.WriteString(sectionRule(t, title, w))
	for _, r := range rows {
		b.WriteByte('\n')
		b.WriteString(t.DimStyle.Render(
			indent() + component.Pad(r[0], labelW),
		))
		b.WriteString(strings.Repeat(" ", layout.Snug))
		b.WriteString(fitValue(r[1], w-len(indent())-labelW-layout.Snug))
	}
	return b.String()
}

// sectionRule is a group heading followed by a faint rule to the pane edge.
func sectionRule(t theme.Theme, label string, w int) string {
	head := t.PaneTitleStyle.Render(label) + " "
	gap := w - ansi.StringWidth(head)
	if gap <= 0 {
		return head
	}
	return head + t.RuleStyle.Render(strings.Repeat("─", gap))
}

// indent is the left margin every row inside a group sits at.
func indent() string { return strings.Repeat(" ", layout.Snug) }

// fitValue truncates an already-styled value to the width left for it, keeping
// the tail of a path — the part that identifies it — rather than the head.
func fitValue(v string, w int) string {
	if w <= 0 || ansi.StringWidth(v) <= w {
		return v
	}
	if strings.Contains(v, "/") {
		return ansi.TruncateLeft(v, ansi.StringWidth(v)-(w-1), "…")
	}
	return ansi.Truncate(v, w, "…")
}

// orEmDash marks a fact the registry left empty, so a row reads as
// deliberately absent rather than as a blank line.
func orEmDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// shortSHA abbreviates a commit to the conventional seven characters.
func shortSHA(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}

// humanSince renders an elapsed duration the way a status line should read: one
// unit, no decimals, biggest unit that is not zero.
func humanSince(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
