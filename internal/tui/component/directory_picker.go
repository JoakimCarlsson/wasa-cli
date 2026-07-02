package component

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

	"github.com/joakimcarlsson/wasa-cli/internal/tui/theme"
)

// maxFilterDepth bounds how deep a fuzzy filter walks below the root and
// filterCap bounds how many matches it collects, so a filter over a large tree
// stays bounded. filterDebounce is how long after the last keystroke the filter
// walk is deferred, so typing fast spawns one walk rather than one per keystroke.
const (
	maxFilterDepth = 6
	filterCap      = 4000
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

// RecentDir is one entry in the picker's recent pane: a directory drawn from
// session and workspace history, with its home-relative display form.
type RecentDir struct {
	Path    string
	Display string
}

// DirChosenMsg is emitted by a DirectoryPicker when the user picks a directory;
// Path is the chosen directory.
type DirChosenMsg struct{ Path string }

// DirCancelledMsg is emitted by a DirectoryPicker when the user dismisses it
// without a choice.
type DirCancelledMsg struct{}

// FilterTickMsg fires after the debounce interval to start a deferred filter
// walk; gen identifies the keystroke that scheduled it so a superseded tick is
// ignored. FilterResultMsg carries a completed filter walk back to the picker.
type FilterTickMsg struct{ Gen int }

// FilterResultMsg carries a completed filter walk back to the picker, tagged
// with the generation that requested it so a superseded result is ignored.
type FilterResultMsg struct {
	gen   int
	root  *treeNode
	count int
}

// DirectoryPicker is the two-pane directory browser shown over the create form.
// The left pane is a lazily-loaded tree rooted at root that you drill into and
// roam upward through, and that typing fuzzy-filters broot-style; the right pane
// is a recent-directories quick list. tab moves focus between panes. The fuzzy
// filter runs off the update goroutine and debounced: a query keystroke bumps
// filterGen and schedules a FilterTickMsg, the tick spawns the walk, and a
// FilterResultMsg is applied only while its gen is still current.
type DirectoryPicker struct {
	theme      theme.Theme
	root       *treeNode
	filterRoot *treeNode
	query      textinput.Model
	visible    []visRow
	cursor     int
	offset     int

	recents      []RecentDir
	recentCursor int
	focus        int

	// creating is the new-folder sub-mode: name is the folder-name prompt shown
	// in place of the filter, createBase is the directory the folder is created
	// under (the highlighted node when the sub-mode was entered), and createErr
	// carries a failed mkdir back to the prompt. The sub-mode lets a brand-new
	// project directory be made without leaving the picker for a shell.
	creating   bool
	name       textinput.Model
	createBase string
	createErr  string

	width  int
	height int
	home   string

	// filterBase is the directory the fuzzy filter searches under. The browse tree
	// is rooted at the filesystem root so the whole ancestor chain is reachable,
	// but the filter stays anchored to the directory the picker opened on, so
	// typing a query scans that subtree rather than the entire filesystem.
	filterBase string

	// Chosen is the path the picker carried in its last DirChosenMsg.
	Chosen string

	filtering    bool
	pending      bool
	pendingQuery string
	filterGen    int
	matchCount   int
}

// NewDirectoryPicker builds the browser with the whole ancestor chain above
// rootPath revealed up to the filesystem root, so the tree shows where you are in
// the larger filesystem and the arrow keys roam up through parents and their
// siblings as naturally as they roam down. The cursor starts on the active
// directory — selectPath when given, otherwise rootPath. recents seeds the recent
// pane; with none the picker shows the tree alone.
func NewDirectoryPicker(
	theme theme.Theme,
	rootPath, selectPath, home string,
	recents []RecentDir,
	width, height int,
) DirectoryPicker {
	q := textinput.New()
	q.Prompt = "> "
	q.Placeholder = "type to fuzzy-filter"
	q.CharLimit = 200
	q.Focus()
	if width > 6 {
		q.Width = width - 4
	}

	name := textinput.New()
	name.Prompt = "> "
	name.Placeholder = "new folder name"
	name.CharLimit = 200
	if width > 6 {
		name.Width = width - 4
	}

	root := &treeNode{
		path:     rootPath,
		name:     filepath.Base(rootPath),
		expanded: true,
		isRepo:   isRepoDir(rootPath),
	}
	loadChildren(root)

	p := DirectoryPicker{
		theme:      theme,
		root:       root,
		query:      q,
		name:       name,
		recents:    recents,
		home:       home,
		width:      width,
		height:     height,
		filterBase: rootPath,
	}
	p.revealToFilesystemRoot()
	target := selectPath
	if target == "" {
		target = rootPath
	}
	p.rebuild()
	p.cursorToPath(target)
	return p
}

// revealToFilesystemRoot re-roots the tree upward one level at a time until it is
// rooted at the filesystem root, leaving the whole ancestor chain expanded and the
// original directory grafted in at its true depth. Each ascendRoot loads only the
// children of the level it climbs to, so revealing the chain is a handful of
// directory reads, not a recursive walk.
func (p *DirectoryPicker) revealToFilesystemRoot() {
	for {
		parent := filepath.Dir(p.root.path)
		if parent == p.root.path {
			return
		}
		p.ascendRoot()
	}
}

// Update handles a key message, returning the updated picker and a command. The
// command emits a DirChosenMsg when a directory is picked or a DirCancelledMsg
// when the picker is dismissed; on a query keystroke it carries the debounced
// filter tick, and otherwise it is nil.
func (p DirectoryPicker) Update(msg tea.Msg) (DirectoryPicker, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}

	if p.creating {
		return p.updateCreating(key)
	}

	switch key.String() {
	case "esc":
		if p.query.Value() != "" {
			p.query.SetValue("")
			return p, p.onQueryChange()
		}
		return p, dirCancelled
	case "tab":
		if len(p.recents) > 0 {
			p.focus = focusTree + focusRecent - p.focus
		}
		return p, nil
	case "enter":
		if path, ok := p.currentPath(); ok {
			p.Chosen = path
			return p, dirChosen(path)
		}
		return p, nil
	case "up", "ctrl+p":
		p.moveCursor(-1)
		return p, nil
	case "down", "ctrl+n":
		p.moveCursor(1)
		return p, nil
	}

	if p.focus == focusTree {
		switch key.String() {
		case "right":
			if !p.filtering {
				p.toggle()
				return p, nil
			}
		case "left":
			if !p.filtering {
				p.collapseOrParent()
				return p, nil
			}
		case "+":
			if !p.filtering {
				return p.beginCreate(), nil
			}
		}
	} else {
		switch key.String() {
		case "left", "right", "-":
			return p, nil
		}
	}

	p.focus = focusTree
	var cmd tea.Cmd
	p.query, cmd = p.query.Update(msg)
	return p, tea.Batch(cmd, p.onQueryChange())
}

// beginCreate enters the new-folder sub-mode, anchoring the folder-to-be under
// the highlighted tree directory (or the root when nothing is selected) and moving
// focus to the name prompt. It is reached only from the tree pane, where the
// selection is always a directory, so createBase is always a valid parent.
func (p DirectoryPicker) beginCreate() DirectoryPicker {
	base, ok := p.currentPath()
	if !ok {
		base = p.root.path
	}
	p.createBase = base
	p.creating = true
	p.createErr = ""
	p.name.SetValue("")
	p.name.Focus()
	p.query.Blur()
	return p
}

// updateCreating handles a keystroke while the new-folder prompt is open. Enter
// makes the directory under createBase and picks it (so a brand-new project is
// created and chosen in one step); esc backs out to browsing; anything else edits
// the name. A mkdir failure is surfaced on the prompt rather than dismissing it,
// so a bad name can be corrected in place.
func (p DirectoryPicker) updateCreating(
	key tea.KeyMsg,
) (DirectoryPicker, tea.Cmd) {
	switch key.String() {
	case "esc":
		p.creating = false
		p.createErr = ""
		p.name.SetValue("")
		p.name.Blur()
		p.query.Focus()
		return p, nil
	case "enter":
		name := strings.TrimSpace(p.name.Value())
		if name == "" {
			p.createErr = "name required"
			return p, nil
		}
		full := filepath.Join(p.createBase, name)
		if err := os.MkdirAll(full, 0o755); err != nil {
			p.createErr = err.Error()
			return p, nil
		}
		p.creating = false
		p.createErr = ""
		p.name.SetValue("")
		p.name.Blur()
		p.Chosen = full
		return p, dirChosen(full)
	}
	var cmd tea.Cmd
	p.name, cmd = p.name.Update(key)
	return p, cmd
}

func dirChosen(path string) tea.Cmd {
	return func() tea.Msg { return DirChosenMsg{Path: path} }
}

func dirCancelled() tea.Msg { return DirCancelledMsg{} }

// onQueryChange reacts to an edited query: it reverts to the browse tree when the
// query is empty, otherwise bumps the filter generation and schedules a debounced
// walk, returning the tick command that will start it.
func (p *DirectoryPicker) onQueryChange() tea.Cmd {
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

// TickFilter starts the deferred filter walk if the tick's generation is still
// the current one; a superseded tick (the user kept typing) is dropped.
func (p *DirectoryPicker) TickFilter(gen int) tea.Cmd {
	if gen != p.filterGen || !p.pending {
		return nil
	}
	return filterWalk(gen, p.filterBase, p.pendingQuery)
}

// ApplyFilterResult installs a completed filter walk, ignoring a result whose
// generation has been superseded by newer typing.
func (p *DirectoryPicker) ApplyFilterResult(msg FilterResultMsg) {
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
		return FilterTickMsg{Gen: gen}
	})
}

func filterWalk(gen int, root, query string) tea.Cmd {
	return func() tea.Msg {
		tree, count := buildFilterTree(root, query)
		return FilterResultMsg{gen: gen, root: tree, count: count}
	}
}

// moveCursor moves the selection in the focused pane.
func (p *DirectoryPicker) moveCursor(delta int) {
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
func (p DirectoryPicker) currentPath() (string, bool) {
	if p.focus == focusRecent {
		if p.recentCursor >= 0 && p.recentCursor < len(p.recents) {
			return p.recents[p.recentCursor].Path, true
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
func (p *DirectoryPicker) toggle() {
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
func (p *DirectoryPicker) collapseOrParent() {
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
func (p *DirectoryPicker) ascendRoot() {
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

func (p *DirectoryPicker) rebuild() { p.flattenFrom(p.root) }

// flattenFrom re-flattens the expanded tree under root into visible in display
// order and clamps the cursor and scroll window to it.
func (p *DirectoryPicker) flattenFrom(root *treeNode) {
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

func (p *DirectoryPicker) cursorToPath(path string) {
	for i, r := range p.visible {
		if r.node.path == path {
			p.cursor = i
			p.ensureVisible()
			return
		}
	}
}

func (p *DirectoryPicker) cursorToFirstMatch() {
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

func (p *DirectoryPicker) ensureVisible() {
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

func (p DirectoryPicker) rows() int {
	return min(PickerRows, max(p.height, 3))
}

// View renders the picker box: the title and filter input, the tree (and recent
// pane when present), and the footer hint.
func (p DirectoryPicker) View() string {
	inner := max(p.width, 32)
	rows := p.rows()

	head := p.headView()
	body := p.bodyView(inner, rows)
	footer := p.theme.DimStyle.Render(p.footer())

	return p.theme.PickerStyle.Render(head + "\n\n" + body + "\n\n" + footer)
}

// headView renders the picker's title and input line. While browsing it is the
// "Pick directory" title over the fuzzy-filter box; in the new-folder sub-mode it
// becomes a "New folder in <dir>" title over the name prompt, with a failed mkdir
// shown beneath.
func (p DirectoryPicker) headView() string {
	if !p.creating {
		return p.theme.TitleStyle.Render("Pick directory") + "\n" +
			p.query.View()
	}
	title := "New folder in " + HomeRel(p.createBase, p.home)
	out := p.theme.TitleStyle.Render(title) + "\n" + p.name.View()
	if p.createErr != "" {
		out += "\n" + p.theme.ErrorStyle.Render("  "+p.createErr)
	}
	return out
}

// bodyView lays out the tree pane, and a recent pane beside it when there are
// recents, sized to fit inner cells across.
func (p DirectoryPicker) bodyView(inner, rows int) string {
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
		sep[i] = p.theme.DimStyle.Render(" │ ")
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
func (p DirectoryPicker) treeLines(w, rows int) []string {
	switch {
	case p.pending && len(p.visible) == 0:
		return []string{p.theme.DimStyle.Render("  searching…")}
	case p.filtering && !p.pending && p.matchCount == 0:
		return []string{p.theme.DimStyle.Render("  no matches")}
	case len(p.visible) == 0:
		return []string{p.theme.DimStyle.Render("  (empty)")}
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
func (p DirectoryPicker) recentLines(w, rows int) []string {
	lines := []string{p.theme.FocusedLabelStyle.Render("Recent")}
	limit := rows - 1
	for i, rec := range p.recents {
		if i >= limit {
			break
		}
		disp := tailTrunc(rec.Display, w-2)
		if p.focus == focusRecent && i == p.recentCursor {
			lines = append(
				lines, p.theme.SelRowTitleStyle.Render(Pad("▌ "+disp, w)),
			)
			continue
		}
		lines = append(lines, "  "+disp)
	}
	return lines
}

func (p DirectoryPicker) footer() string {
	switch {
	case p.creating:
		return "type name · ↵ create · esc cancel"
	case p.pending:
		return "searching… · ↵ pick · esc clear"
	case p.filtering:
		return strconv.Itoa(p.matchCount) +
			" matches · ↑↓ move · ↵ pick · esc clear"
	}
	if len(p.recents) > 0 {
		return "type to filter · ↑↓ move · ⇥ pane · → expand · " +
			"+ new · ↵ pick · esc"
	}
	return "type to filter · ↑↓ move · → expand · ← collapse · " +
		"+ new · ↵ pick · esc"
}

// row renders one tree line: a two-cell gutter (a selection bar when current),
// indentation by depth, an expand marker, and the directory name. The root line
// shows its home-relative path; a fuzzy match has its matched characters
// highlighted, otherwise a repository is accented. The current row is a solid
// selection band sized to w so it never wraps.
func (p DirectoryPicker) row(r visRow, current bool, w int) string {
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
		label = HomeRel(n.path, p.home)
	}

	if current {
		return p.theme.SelRowTitleStyle.Render(
			Pad("▌ "+indent+marker+label, w),
		)
	}

	var styledLabel string
	switch {
	case n.matched:
		styledLabel = Highlight(p.theme, label, n.positions)
	case n.isRepo:
		styledLabel = p.theme.FocusedLabelStyle.Render(label)
	default:
		styledLabel = label
	}
	line := "  " + indent + p.theme.DimStyle.Render(marker) + styledLabel
	return ansi.Truncate(line, w, "…") + "\x1b[0m"
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
			if _, pos, ok := FuzzyScore(query, name); ok {
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
