package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// pickerRows is the most tree rows shown at once; the view scrolls a window of
// this height over the visible nodes. maxFilterDepth bounds how deep a fuzzy
// filter walks below the root and filterCap bounds how many matches it collects,
// so a filter over a large tree stays bounded; maxRecents caps the recent list.
// filterDebounce is how long after the last keystroke the filter walk is
// deferred, so typing fast spawns one walk rather than one per keystroke.
const (
	pickerRows     = 14
	maxFilterDepth = 6
	filterCap      = 4000
	maxRecents     = 8
	filterDebounce = 150 * time.Millisecond
)

const (
	focusTree = iota
	focusRecent
)

// scanSkip is the set of directory names the browser never lists: dependency and
// build caches that are large, deep and never themselves a directory you want to
// start a session in. Hidden directories (those starting with a dot) are skipped
// separately.
var scanSkip = map[string]bool{
	"node_modules": true,
	"Library":      true,
	"vendor":       true,
	"target":       true,
	"dist":         true,
	"build":        true,
	"Pods":         true,
}

// treeNode is one directory in the browser's tree. In browse mode children are
// loaded lazily on first expand: until then loaded is false and the node draws
// as collapsible. isLeaf is set once loaded with no sub-directories; isRepo
// marks a directory holding a .git. matched and positions are set only on the
// filtered tree, marking a fuzzy hit and the characters of its name that matched.
type treeNode struct {
	path      string
	name      string
	parent    *treeNode
	children  []*treeNode
	expanded  bool
	loaded    bool
	isLeaf    bool
	isRepo    bool
	matched   bool
	positions []int
}

// visRow is a node placed in the flattened, on-screen order with the indentation
// depth it is drawn at.
type visRow struct {
	node  *treeNode
	depth int
}

// recentDir is one entry in the picker's recent pane: a directory drawn from
// session and workspace history, with its home-relative display form.
type recentDir struct {
	path    string
	display string
}

// dirPickerResult is what a picker update reports back to the parent model.
type dirPickerResult int

const (
	pickNone dirPickerResult = iota
	pickChoose
	pickCancel
)

// filterTickMsg fires after the debounce interval to start a deferred filter
// walk; gen identifies the keystroke that scheduled it so a superseded tick is
// ignored. filterResultMsg carries a completed filter walk back to the picker.
type filterTickMsg struct{ gen int }

type filterResultMsg struct {
	gen   int
	root  *treeNode
	count int
}

// dirPicker is the two-pane directory browser shown over the create form. The
// left pane is a lazily-loaded tree rooted at root that you drill into and roam
// upward through, and that typing fuzzy-filters broot-style; the right pane is a
// recent-directories quick list. tab moves focus between panes. The fuzzy filter
// runs off the update goroutine and debounced: a query keystroke bumps filterGen
// and schedules a filterTickMsg, the tick spawns the walk, and a filterResultMsg
// is applied only while its gen is still current.
type dirPicker struct {
	root       *treeNode
	filterRoot *treeNode
	query      textinput.Model
	visible    []visRow
	cursor     int
	offset     int

	recents      []recentDir
	recentCursor int
	focus        int

	width  int
	height int
	home   string
	chosen string

	filtering    bool
	pending      bool
	pendingQuery string
	filterGen    int
	matchCount   int
}

// newDirPicker builds a tree rooted at rootPath with its top level loaded and an
// empty filter. When selectPath names one of the root's children the cursor
// starts on it. recents seeds the recent pane; with none the picker shows the
// tree alone.
func newDirPicker(
	rootPath, selectPath, home string,
	recents []recentDir,
	width, height int,
) dirPicker {
	q := textinput.New()
	q.Prompt = "> "
	q.Placeholder = "type to fuzzy-filter"
	q.CharLimit = 200
	q.Focus()
	if width > 6 {
		q.Width = width - 4
	}

	root := &treeNode{
		path:     rootPath,
		name:     filepath.Base(rootPath),
		expanded: true,
		isRepo:   isRepoDir(rootPath),
	}
	loadChildren(root)

	p := dirPicker{
		root:    root,
		query:   q,
		recents: recents,
		home:    home,
		width:   width,
		height:  height,
	}
	p.rebuild()
	if selectPath != "" {
		p.cursorToPath(selectPath)
	}
	return p
}

func (p dirPicker) update(msg tea.Msg) (dirPicker, dirPickerResult, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, pickNone, nil
	}

	switch key.String() {
	case "esc":
		if p.query.Value() != "" {
			p.query.SetValue("")
			return p, pickNone, p.onQueryChange()
		}
		return p, pickCancel, nil
	case "tab":
		if len(p.recents) > 0 {
			p.focus = focusTree + focusRecent - p.focus
		}
		return p, pickNone, nil
	case "enter":
		if path, ok := p.currentPath(); ok {
			p.chosen = path
			return p, pickChoose, nil
		}
		return p, pickNone, nil
	case "up", "ctrl+p":
		p.moveCursor(-1)
		return p, pickNone, nil
	case "down", "ctrl+n":
		p.moveCursor(1)
		return p, pickNone, nil
	}

	if p.focus == focusTree {
		switch key.String() {
		case "right":
			if !p.filtering {
				p.toggle()
				return p, pickNone, nil
			}
		case "left":
			if !p.filtering {
				p.collapseOrParent()
				return p, pickNone, nil
			}
		case "-":
			if !p.filtering {
				p.ascendRoot()
				return p, pickNone, nil
			}
		}
	} else {
		switch key.String() {
		case "left", "right", "-":
			return p, pickNone, nil
		}
	}

	p.focus = focusTree
	var cmd tea.Cmd
	p.query, cmd = p.query.Update(msg)
	return p, pickNone, tea.Batch(cmd, p.onQueryChange())
}

// onQueryChange reacts to an edited query: it reverts to the browse tree when the
// query is empty, otherwise bumps the filter generation and schedules a debounced
// walk, returning the tick command that will start it.
func (p *dirPicker) onQueryChange() tea.Cmd {
	p.filterGen++
	q := strings.TrimSpace(p.query.Value())
	if q == "" {
		p.filtering = false
		p.pending = false
		p.filterRoot = nil
		p.matchCount = 0
		p.rebuild()
		return nil
	}
	p.filtering = true
	p.pending = true
	p.pendingQuery = q
	return filterTick(p.filterGen)
}

// tickFilter starts the deferred filter walk if the tick's generation is still
// the current one; a superseded tick (the user kept typing) is dropped.
func (p *dirPicker) tickFilter(gen int) tea.Cmd {
	if gen != p.filterGen || !p.pending {
		return nil
	}
	return filterWalk(gen, p.root.path, p.pendingQuery)
}

// applyFilterResult installs a completed filter walk, ignoring a result whose
// generation has been superseded by newer typing.
func (p *dirPicker) applyFilterResult(msg filterResultMsg) {
	if msg.gen != p.filterGen {
		return
	}
	p.pending = false
	p.filterRoot = msg.root
	p.matchCount = msg.count
	p.flattenFrom(msg.root)
	p.cursorToFirstMatch()
}

func filterTick(gen int) tea.Cmd {
	return tea.Tick(filterDebounce, func(time.Time) tea.Msg {
		return filterTickMsg{gen: gen}
	})
}

func filterWalk(gen int, root, query string) tea.Cmd {
	return func() tea.Msg {
		tree, count := buildFilterTree(root, query)
		return filterResultMsg{gen: gen, root: tree, count: count}
	}
}

// moveCursor moves the selection in the focused pane.
func (p *dirPicker) moveCursor(delta int) {
	if p.focus == focusRecent {
		if len(p.recents) == 0 {
			return
		}
		p.recentCursor = min(max(p.recentCursor+delta, 0), len(p.recents)-1)
		return
	}
	if len(p.visible) == 0 {
		return
	}
	p.cursor = min(max(p.cursor+delta, 0), len(p.visible)-1)
	p.ensureVisible()
}

// currentPath is the path the focused pane's selection would pick.
func (p dirPicker) currentPath() (string, bool) {
	if p.focus == focusRecent {
		if p.recentCursor >= 0 && p.recentCursor < len(p.recents) {
			return p.recents[p.recentCursor].path, true
		}
		return "", false
	}
	if p.cursor >= 0 && p.cursor < len(p.visible) {
		return p.visible[p.cursor].node.path, true
	}
	return "", false
}

// toggle expands the node under the cursor, loading its children on first use,
// or collapses it when already expanded. A directory with no sub-directories is
// a leaf and does not expand. Browse mode only.
func (p *dirPicker) toggle() {
	if len(p.visible) == 0 {
		return
	}
	n := p.visible[p.cursor].node
	if n.expanded {
		n.expanded = false
		p.rebuild()
		return
	}
	loadChildren(n)
	if !n.isLeaf {
		n.expanded = true
		p.rebuild()
	}
}

// collapseOrParent collapses an expanded node, or jumps the cursor to the parent
// of a collapsed one. Browse mode only.
func (p *dirPicker) collapseOrParent() {
	if len(p.visible) == 0 {
		return
	}
	n := p.visible[p.cursor].node
	if n.expanded {
		n.expanded = false
		p.rebuild()
		return
	}
	if n.parent != nil {
		p.cursorToPath(n.parent.path)
	}
}

// ascendRoot re-roots the tree one directory up, grafting the old root in as a
// child so its expanded state and loaded children survive, and parks the cursor
// back on it. Browse mode only.
func (p *dirPicker) ascendRoot() {
	parent := filepath.Dir(p.root.path)
	if parent == p.root.path {
		return
	}
	old := p.root
	old.isRepo = isRepoDir(old.path)
	newRoot := &treeNode{
		path:     parent,
		name:     filepath.Base(parent),
		expanded: true,
		isRepo:   isRepoDir(parent),
	}
	loadChildren(newRoot)
	for i, c := range newRoot.children {
		if c.path == old.path {
			old.parent = newRoot
			old.expanded = true
			newRoot.children[i] = old
			break
		}
	}
	p.root = newRoot
	p.rebuild()
	p.cursorToPath(old.path)
}

func (p *dirPicker) rebuild() { p.flattenFrom(p.root) }

// flattenFrom re-flattens the expanded tree under root into visible in display
// order and clamps the cursor and scroll window to it.
func (p *dirPicker) flattenFrom(root *treeNode) {
	p.visible = p.visible[:0]
	var walk func(n *treeNode, depth int)
	walk = func(n *treeNode, depth int) {
		p.visible = append(p.visible, visRow{node: n, depth: depth})
		if n.expanded {
			for _, c := range n.children {
				walk(c, depth+1)
			}
		}
	}
	if root != nil {
		walk(root, 0)
	}
	p.cursor = min(max(p.cursor, 0), max(len(p.visible)-1, 0))
	p.ensureVisible()
}

func (p *dirPicker) cursorToPath(path string) {
	for i, r := range p.visible {
		if r.node.path == path {
			p.cursor = i
			p.ensureVisible()
			return
		}
	}
}

func (p *dirPicker) cursorToFirstMatch() {
	for i, r := range p.visible {
		if r.node.matched {
			p.cursor = i
			p.ensureVisible()
			return
		}
	}
	p.cursor = 0
	p.ensureVisible()
}

func (p *dirPicker) ensureVisible() {
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

func (p dirPicker) rows() int {
	return min(pickerRows, max(p.height, 3))
}

func (p dirPicker) view() string {
	inner := max(p.width, 32)
	rows := p.rows()

	head := titleStyle.Render("Pick directory") + "\n" + p.query.View()
	body := p.bodyView(inner, rows)
	footer := dimStyle.Render(p.footer())

	return pickerStyle.Render(head + "\n\n" + body + "\n\n" + footer)
}

// bodyView lays out the tree pane, and a recent pane beside it when there are
// recents, sized to fit inner cells across.
func (p dirPicker) bodyView(inner, rows int) string {
	if len(p.recents) == 0 {
		return strings.Join(
			fitColumn(p.treeLines(inner, rows), inner, rows),
			"\n",
		)
	}

	recentW := min(34, inner/2)
	sepW := 3
	treeW := inner - recentW - sepW

	tree := fitColumn(p.treeLines(treeW, rows), treeW, rows)
	recent := fitColumn(p.recentLines(recentW, rows), recentW, rows)
	sep := make([]string, rows)
	for i := range sep {
		sep[i] = dimStyle.Render(" │ ")
	}

	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		strings.Join(tree, "\n"),
		strings.Join(sep, "\n"),
		strings.Join(recent, "\n"),
	)
}

// treeLines renders the tree pane's rows for the current scroll window, or a
// single status line when there is nothing to show.
func (p dirPicker) treeLines(w, rows int) []string {
	switch {
	case p.pending && len(p.visible) == 0:
		return []string{dimStyle.Render("  searching…")}
	case p.filtering && !p.pending && p.matchCount == 0:
		return []string{dimStyle.Render("  no matches")}
	case len(p.visible) == 0:
		return []string{dimStyle.Render("  (empty)")}
	}
	end := min(p.offset+rows, len(p.visible))
	lines := make([]string, 0, end-p.offset)
	for i := p.offset; i < end; i++ {
		current := p.focus == focusTree && i == p.cursor
		lines = append(lines, p.row(p.visible[i], current, w))
	}
	return lines
}

// recentLines renders the recent pane: a header followed by the recent
// directories, the focused one drawn as a selection band.
func (p dirPicker) recentLines(w, rows int) []string {
	lines := []string{focusedLabelStyle.Render("Recent")}
	limit := rows - 1
	for i, rec := range p.recents {
		if i >= limit {
			break
		}
		disp := tailTrunc(rec.display, w-2)
		if p.focus == focusRecent && i == p.recentCursor {
			lines = append(lines, selRowTitleStyle.Render(pad("▌ "+disp, w)))
			continue
		}
		lines = append(lines, "  "+disp)
	}
	return lines
}

func (p dirPicker) footer() string {
	switch {
	case p.pending:
		return "searching… · ↵ pick · esc clear"
	case p.filtering:
		return strconv.Itoa(p.matchCount) +
			" matches · ↑↓ move · ↵ pick · esc clear"
	}
	if len(p.recents) > 0 {
		return "type to filter · ↑↓ move · ⇥ pane · → expand · ↵ pick · esc"
	}
	return "type to filter · ↑↓ move · → expand · ← collapse · " +
		"- up · ↵ pick · esc"
}

// row renders one tree line: a two-cell gutter (a selection bar when current),
// indentation by depth, an expand marker, and the directory name. The root line
// shows its home-relative path; a fuzzy match has its matched characters
// highlighted, otherwise a repository is accented. The current row is a solid
// selection band sized to w so it never wraps.
func (p dirPicker) row(r visRow, current bool, w int) string {
	n := r.node
	indent := strings.Repeat("  ", r.depth)

	marker := "▸ "
	switch {
	case n.expanded && len(n.children) > 0:
		marker = "▾ "
	case n.loaded && n.isLeaf:
		marker = "  "
	}

	label := n.name
	if r.depth == 0 {
		label = homeRel(n.path, p.home)
	}

	if current {
		return selRowTitleStyle.Render(pad("▌ "+indent+marker+label, w))
	}

	var styledLabel string
	switch {
	case n.matched:
		styledLabel = highlight(label, n.positions)
	case n.isRepo:
		styledLabel = focusedLabelStyle.Render(label)
	default:
		styledLabel = label
	}
	line := "  " + indent + dimStyle.Render(marker) + styledLabel
	return ansi.Truncate(line, w, "…") + "\x1b[0m"
}

// fitColumn pads every line to exactly w visible cells and the block to exactly
// height lines, so columns align when joined horizontally.
func fitColumn(lines []string, w, height int) []string {
	out := make([]string, height)
	for i := range out {
		if i < len(lines) {
			out[i] = fitAnsi(lines[i], w)
		} else {
			out[i] = strings.Repeat(" ", w)
		}
	}
	return out
}

// fitAnsi pads or truncates an ANSI-styled string to exactly w visible cells.
func fitAnsi(s string, w int) string {
	vis := ansi.StringWidth(s)
	if vis > w {
		return ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", w-vis)
}

// tailTrunc keeps the rightmost w visible cells of a plain string, prefixing an
// ellipsis when it had to cut — so a path keeps its tail, the directory name.
func tailTrunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	vis := ansi.StringWidth(s)
	if vis <= w {
		return s
	}
	return ansi.TruncateLeft(s, vis-(w-1), "…")
}

// highlight styles label rune by rune, accenting the byte offsets listed in
// positions and leaving the rest plain, so a fuzzy match reads as the query
// characters lit up inside the name.
func highlight(label string, positions []int) string {
	if len(positions) == 0 {
		return label
	}
	set := make(map[int]bool, len(positions))
	for _, pos := range positions {
		set[pos] = true
	}
	var b strings.Builder
	for i, r := range label {
		if set[i] {
			b.WriteString(matchStyle.Render(string(r)))
		} else {
			b.WriteString(string(r))
		}
	}
	return b.String()
}

// loadChildren reads dir's sub-directories into n on first call, sorted by name,
// skipping hidden directories and known heavy caches and recording which are git
// repositories. A directory that cannot be read, or holds no sub-directories,
// becomes a leaf. Files are ignored — only directories can host a session.
func loadChildren(n *treeNode) {
	if n.loaded {
		return
	}
	n.loaded = true

	kids := readSubdirs(n.path, n)
	sortNodes(kids)
	n.children = kids
	n.isLeaf = len(kids) == 0
}

// readSubdirs returns the listable sub-directories of dir as nodes parented to
// parent, skipping hidden directories, known caches and non-directories.
func readSubdirs(dir string, parent *treeNode) []*treeNode {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var kids []*treeNode
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || scanSkip[name] {
			continue
		}
		full := filepath.Join(dir, name)
		kids = append(kids, &treeNode{
			path:   full,
			name:   name,
			parent: parent,
			isRepo: isRepoDir(full),
		})
	}
	return kids
}

func sortNodes(nodes []*treeNode) {
	sort.Slice(nodes, func(i, j int) bool {
		return strings.ToLower(nodes[i].name) < strings.ToLower(nodes[j].name)
	})
}

// buildFilterTree walks below rootPath collecting directories whose name fuzzy-
// matches query, then assembles a tree of those matches and the ancestor chain
// each hangs from, all expanded. It returns the tree's root and the number of
// matched directories.
func buildFilterTree(rootPath, query string) (*treeNode, int) {
	root := &treeNode{
		path:     rootPath,
		name:     filepath.Base(rootPath),
		expanded: true,
		loaded:   true,
		isRepo:   isRepoDir(rootPath),
	}
	nodes := map[string]*treeNode{rootPath: root}

	count := 0
	for _, h := range collectHits(rootPath, query) {
		n := ensureNode(h.path, nodes)
		if !n.matched {
			count++
		}
		n.matched = true
		n.positions = h.positions
	}
	finalizeTree(root)
	return root, count
}

// fhit is a fuzzy match found while walking the filter tree: the matched
// directory and the byte offsets of its name that matched.
type fhit struct {
	path      string
	positions []int
}

// collectHits walks the directory tree below rootPath, depth- and count-bounded,
// returning every directory whose base name fuzzy-matches query.
func collectHits(rootPath, query string) []fhit {
	var hits []fhit
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if len(hits) >= filterCap {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasPrefix(name, ".") || scanSkip[name] {
				continue
			}
			full := filepath.Join(dir, name)
			if _, pos, ok := fuzzyScore(query, name); ok {
				hits = append(hits, fhit{path: full, positions: pos})
			}
			if depth < maxFilterDepth {
				walk(full, depth+1)
			}
		}
	}
	walk(rootPath, 0)
	return hits
}

// ensureNode returns the node for path, creating it and any missing ancestors up
// to the pre-seeded root and linking them as children along the way.
func ensureNode(path string, nodes map[string]*treeNode) *treeNode {
	if n, ok := nodes[path]; ok {
		return n
	}
	parent := ensureNode(filepath.Dir(path), nodes)
	n := &treeNode{
		path:     path,
		name:     filepath.Base(path),
		parent:   parent,
		expanded: true,
		loaded:   true,
		isRepo:   isRepoDir(path),
	}
	parent.children = append(parent.children, n)
	nodes[path] = n
	return n
}

// finalizeTree sorts every level of a filtered tree by name and records which
// nodes are leaves, so the expand markers render correctly.
func finalizeTree(n *treeNode) {
	sortNodes(n.children)
	n.isLeaf = len(n.children) == 0
	n.loaded = true
	for _, c := range n.children {
		finalizeTree(c)
	}
}

// isRepoDir reports whether path holds a .git entry, marking it a git
// repository.
func isRepoDir(path string) bool {
	_, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil
}

// homeRel rewrites a path under home to a ~-prefixed form for display, leaving
// paths outside home untouched.
func homeRel(path, home string) string {
	if home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rel, ok := strings.CutPrefix(path, home+string(os.PathSeparator)); ok {
		return "~" + string(os.PathSeparator) + rel
	}
	return path
}

// fuzzyScore matches query against target as a case-insensitive subsequence,
// returning the match score, the byte positions in target that matched, and
// whether every query character was found in order. Consecutive matches and
// matches at a word boundary score higher. An empty query matches everything.
func fuzzyScore(query, target string) (int, []int, bool) {
	if query == "" {
		return 0, nil, true
	}
	q := strings.ToLower(query)
	t := strings.ToLower(target)

	positions := make([]int, 0, len(q))
	score, ti, prev := 0, 0, -2
	for qi := range len(q) {
		idx := strings.IndexByte(t[ti:], q[qi])
		if idx < 0 {
			return 0, nil, false
		}
		pos := ti + idx
		score++
		if pos == prev+1 {
			score += 5
		}
		if pos == 0 || isBoundary(target[pos-1]) {
			score += 10
		}
		positions = append(positions, pos)
		prev = pos
		ti = pos + 1
	}
	return score, positions, true
}

func isBoundary(b byte) bool {
	return b == '-' || b == '_' || b == ' ' || b == os.PathSeparator
}
