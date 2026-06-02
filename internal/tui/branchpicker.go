package tui

import (
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// branchMatch is a branch name that passed the current fuzzy filter, with the
// byte positions of its name that matched so they can be highlighted.
type branchMatch struct {
	name      string
	positions []int
}

// branchPicker is the branch counterpart to the directory browser: a flat,
// fuzzy-filtered list of the workspace repository's branches shown over the
// create form. Unlike the directory tree it is a single synchronous list — a
// repository's branches are few and already in memory — and it doubles as a
// new-branch entry: with a query that matches nothing, enter chooses the typed
// text so a worktree can be created on a fresh branch.
type branchPicker struct {
	theme   Theme
	query   textinput.Model
	all     []string
	matches []branchMatch
	cursor  int
	offset  int
	width   int
	height  int
	chosen  string
}

func newBranchPicker(
	theme Theme, branches []string, width, height int,
) branchPicker {
	q := textinput.New()
	q.Prompt = "> "
	q.Placeholder = "filter, or type a new branch"
	q.CharLimit = 200
	q.Focus()
	if width > 6 {
		q.Width = width - 4
	}

	p := branchPicker{
		theme:  theme,
		query:  q,
		all:    branches,
		width:  width,
		height: height,
	}
	p.filter()
	return p
}

func (p branchPicker) update(
	msg tea.Msg,
) (branchPicker, dirPickerResult, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, pickNone, nil
	}

	switch key.String() {
	case "esc":
		return p, pickCancel, nil
	case "enter":
		if m := p.selected(); m != nil {
			p.chosen = m.name
			return p, pickChoose, nil
		}
		if q := strings.TrimSpace(p.query.Value()); q != "" {
			p.chosen = q
			return p, pickChoose, nil
		}
		return p, pickNone, nil
	case "up", "ctrl+p":
		p.move(-1)
		return p, pickNone, nil
	case "down", "ctrl+n":
		p.move(1)
		return p, pickNone, nil
	}

	var cmd tea.Cmd
	p.query, cmd = p.query.Update(msg)
	p.filter()
	return p, pickNone, cmd
}

func (p *branchPicker) selected() *branchMatch {
	if p.cursor < 0 || p.cursor >= len(p.matches) {
		return nil
	}
	return &p.matches[p.cursor]
}

func (p *branchPicker) move(delta int) {
	if len(p.matches) == 0 {
		return
	}
	p.cursor = min(max(p.cursor+delta, 0), len(p.matches)-1)
	p.ensureVisible()
}

// filter recomputes the matches for the current query. An empty query keeps the
// branches in their incoming order — git's most-recently-committed first — while
// a non-empty query fuzzy-matches and ranks by score, so the order only reorders
// when the user is actually searching.
func (p *branchPicker) filter() {
	q := strings.TrimSpace(p.query.Value())
	matches := make([]branchMatch, 0, len(p.all))
	if q == "" {
		for _, name := range p.all {
			matches = append(matches, branchMatch{name: name})
		}
		p.matches = matches
		p.cursor, p.offset = 0, 0
		return
	}

	type scored struct {
		m     branchMatch
		score int
	}
	var hits []scored
	for _, name := range p.all {
		if score, pos, ok := fuzzyScore(q, name); ok {
			hits = append(hits, scored{branchMatch{name, pos}, score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].m.name < hits[j].m.name
	})
	for _, h := range hits {
		matches = append(matches, h.m)
	}
	p.matches = matches
	p.cursor, p.offset = 0, 0
}

func (p *branchPicker) ensureVisible() {
	rows := p.rows()
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+rows {
		p.offset = p.cursor - rows + 1
	}
	if p.offset < 0 {
		p.offset = 0
	}
}

func (p branchPicker) rows() int {
	return min(pickerRows, max(p.height, 3))
}

func (p branchPicker) view() string {
	inner := max(p.width, 24)
	rows := p.rows()

	var b strings.Builder
	b.WriteString(p.theme.TitleStyle.Render("Pick branch"))
	b.WriteString("\n")
	b.WriteString(p.query.View())
	b.WriteString("\n\n")

	switch {
	case len(p.all) == 0 && len(p.matches) == 0:
		b.WriteString(
			p.theme.DimStyle.Render("  no branches — type to create one"),
		)
	case len(p.matches) == 0:
		b.WriteString(
			p.theme.DimStyle.Render("  no match — ↵ creates this branch"),
		)
	default:
		end := min(p.offset+rows, len(p.matches))
		for i := p.offset; i < end; i++ {
			b.WriteString(p.row(p.matches[i], i == p.cursor, inner))
			if i < end-1 {
				b.WriteString("\n")
			}
		}
	}

	b.WriteString("\n\n")
	b.WriteString(p.theme.DimStyle.Render(p.footer()))
	return p.theme.PickerStyle.Render(b.String())
}

func (p branchPicker) footer() string {
	return strconv.Itoa(len(p.matches)) +
		"/" + strconv.Itoa(len(p.all)) +
		" · ↑↓ move · ↵ pick/create · esc"
}

// row renders one branch line: a selection band when current, otherwise the
// branch name with its matched characters highlighted.
func (p branchPicker) row(m branchMatch, current bool, w int) string {
	if current {
		return p.theme.SelRowTitleStyle.Render(pad("▌ "+m.name, w))
	}
	line := "  " + highlight(p.theme, m.name, m.positions)
	return ansi.Truncate(line, w, "…") + "\x1b[0m"
}
