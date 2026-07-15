package component

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/joakimcarlsson/wasa-cli/internal/config"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/theme"
)

// testTheme is the resolved default theme, used by the picker tests that build a
// picker directly.
func testTheme() theme.Theme { return theme.NewTheme(config.Default().Theme) }

// pickerTree lays out a small directory tree under a temp root and returns the
// root path:
//
//	root/
//	  alpha/        (a git repo)
//	    nested/
//	  beta/
//	  .hidden/      (skipped: hidden)
//	  node_modules/ (skipped: cache)
//	  file.txt      (skipped: not a directory)
func pickerTree(t *testing.T) string {
	t.Helper()
	root := resolvePath(t.TempDir())
	mustMkdir(t, filepath.Join(root, "alpha", ".git"))
	mustMkdir(t, filepath.Join(root, "alpha", "nested"))
	mustMkdir(t, filepath.Join(root, "beta"))
	mustMkdir(t, filepath.Join(root, ".hidden"))
	mustMkdir(t, filepath.Join(root, "node_modules"))
	if err := os.WriteFile(
		filepath.Join(root, "file.txt"),
		nil,
		0o644,
	); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return root
}

func keyRunes(s string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Text: s, Code: []rune(s)[0]}
}

// visiblePaths is the on-screen node paths in display order.
func visiblePaths(p DirectoryPicker) []string {
	out := make([]string, len(p.visible))
	for i, r := range p.visible {
		out[i] = r.node.path
	}
	return out
}

// fsRoot is the filesystem root reached by climbing parents from dir, the root
// the picker reveals every tree up to.
func fsRoot(dir string) string {
	for filepath.Dir(dir) != dir {
		dir = filepath.Dir(dir)
	}
	return dir
}

// findNode returns the visible tree node at path, or nil when it is not on
// screen.
func findNode(p DirectoryPicker, path string) *treeNode {
	for _, r := range p.visible {
		if r.node.path == path {
			return r.node
		}
	}
	return nil
}

// childNames is the names of n's loaded children, in order.
func childNames(n *treeNode) []string {
	out := make([]string, len(n.children))
	for i, c := range n.children {
		out[i] = c.name
	}
	return out
}

func TestNewDirPickerRevealsChainAndSkipsNoise(t *testing.T) {
	root := pickerTree(t)
	p := NewDirectoryPicker(testTheme(), root, "", root, nil, 60, 14)

	if want := fsRoot(root); p.root.path != want {
		t.Fatalf("tree root = %q, want filesystem root %q", p.root.path, want)
	}

	node := findNode(p, root)
	if node == nil {
		t.Fatalf("active dir %q not revealed in the tree", root)
	}
	got := childNames(node)
	want := []string{"alpha", "beta"}
	if len(got) != len(want) {
		t.Fatalf(
			"children = %v, want %v (hidden/cache/files skipped)",
			got,
			want,
		)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("children[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	if cur, ok := p.currentPath(); !ok || cur != root {
		t.Errorf("cursor on %q, want the active dir %q", cur, root)
	}
}

func TestNewDirPickerMarksRepo(t *testing.T) {
	root := pickerTree(t)
	p := NewDirectoryPicker(testTheme(), root, "", root, nil, 60, 14)

	node := findNode(p, root)
	if node == nil {
		t.Fatalf("active dir %q not revealed in the tree", root)
	}
	var alpha, beta *treeNode
	for _, c := range node.children {
		switch c.name {
		case "alpha":
			alpha = c
		case "beta":
			beta = c
		}
	}
	if alpha == nil || !alpha.isRepo {
		t.Errorf("alpha.isRepo = %v, want true", alpha != nil && alpha.isRepo)
	}
	if beta == nil || beta.isRepo {
		t.Errorf("beta.isRepo = %v, want false", beta != nil && beta.isRepo)
	}
}

func TestNewDirPickerSelectsPath(t *testing.T) {
	root := pickerTree(t)
	beta := filepath.Join(root, "beta")
	p := NewDirectoryPicker(testTheme(), root, beta, root, nil, 60, 14)

	if p.visible[p.cursor].node.path != beta {
		t.Errorf("cursor on %q, want %q", p.visible[p.cursor].node.path, beta)
	}
}

func keyDown() tea.KeyPressMsg  { return tea.KeyPressMsg{Code: tea.KeyDown} }
func keyRight() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyRight} }
func keyEnter() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyEnter} }

// runCmd runs cmd and returns its message, or nil when cmd is nil.
func runCmd(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

// runFilter drives the picker's debounced async filter to completion: it runs
// the deferred walk for the current generation and applies the result, the same
// sequence the model performs in response to the tick and result messages.
func runFilter(t *testing.T, p DirectoryPicker) DirectoryPicker {
	t.Helper()
	cmd := (&p).TickFilter(p.filterGen)
	if cmd == nil {
		t.Fatal("no filter walk was scheduled")
	}
	msg, ok := cmd().(FilterResultMsg)
	if !ok {
		t.Fatalf("filter walk produced %T, want FilterResultMsg", cmd())
	}
	(&p).ApplyFilterResult(msg)
	return p
}

func TestDirPickerExpandRevealsChildren(t *testing.T) {
	root := pickerTree(t)
	p := NewDirectoryPicker(testTheme(), root, "", root, nil, 60, 14)

	p, _ = p.Update(keyDown())
	p, _ = p.Update(keyRight())

	want := filepath.Join(root, "alpha", "nested")
	found := false
	for _, path := range visiblePaths(p) {
		if path == want {
			found = true
		}
	}
	if !found {
		t.Fatalf(
			"expanding alpha did not reveal %q; visible = %v",
			want,
			visiblePaths(p),
		)
	}
}

func TestDirPickerCollapse(t *testing.T) {
	root := pickerTree(t)
	p := NewDirectoryPicker(testTheme(), root, "", root, nil, 60, 14)

	p, _ = p.Update(keyDown())
	p, _ = p.Update(keyRight())
	expanded := len(p.visible)
	p, _ = p.Update(keyRight())

	if len(p.visible) >= expanded {
		t.Errorf(
			"collapse did not shrink visible (%d -> %d)",
			expanded,
			len(p.visible),
		)
	}
}

func TestDirPickerChooseReportsPath(t *testing.T) {
	root := pickerTree(t)
	p := NewDirectoryPicker(testTheme(), root, "", root, nil, 60, 14)

	p, _ = p.Update(keyDown())
	p, cmd := p.Update(keyEnter())

	want := filepath.Join(root, "alpha")
	msg, ok := runCmd(cmd).(DirChosenMsg)
	if !ok {
		t.Fatalf("enter emitted %T, want DirChosenMsg", runCmd(cmd))
	}
	if msg.Path != want {
		t.Errorf("chosen path = %q, want %q", msg.Path, want)
	}
	if p.Chosen != want {
		t.Errorf("chosen = %q, want %q", p.Chosen, want)
	}
}

// TestDirPickerNewFolderCreatesAndPicks checks the new-folder sub-mode: pressing
// "+" on a directory, typing a name and pressing enter makes that directory on
// disk under the highlighted node and picks it, so a brand-new project folder can
// be created without leaving the picker.
func TestDirPickerNewFolderCreatesAndPicks(t *testing.T) {
	root := pickerTree(t)
	p := NewDirectoryPicker(testTheme(), root, "", root, nil, 60, 14)

	p, _ = p.Update(keyRunes("+"))
	if !p.creating {
		t.Fatal("+ did not enter the new-folder sub-mode")
	}

	for _, r := range "fresh-thing" {
		p, _ = p.Update(keyRunes(string(r)))
	}

	p, cmd := p.Update(keyEnter())
	want := filepath.Join(root, "fresh-thing")
	msg, ok := runCmd(cmd).(DirChosenMsg)
	if !ok {
		t.Fatalf("enter emitted %T, want DirChosenMsg", runCmd(cmd))
	}
	if msg.Path != want {
		t.Errorf("chosen path = %q, want %q", msg.Path, want)
	}
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("new folder was not created on disk: %v", err)
	}
	if p.creating {
		t.Error("picker stayed in the new-folder sub-mode after creating")
	}
}

// TestDirPickerNewFolderEscCancels checks that esc backs out of the new-folder
// sub-mode without creating anything.
func TestDirPickerNewFolderEscCancels(t *testing.T) {
	root := pickerTree(t)
	p := NewDirectoryPicker(testTheme(), root, "", root, nil, 60, 14)

	p, _ = p.Update(keyRunes("+"))
	for _, r := range "ghost" {
		p, _ = p.Update(keyRunes(string(r)))
	}
	p, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEsc})

	if p.creating {
		t.Fatal("esc did not leave the new-folder sub-mode")
	}
	if _, ok := runCmd(cmd).(DirChosenMsg); ok {
		t.Fatal("esc in the new-folder sub-mode picked a path")
	}
	if _, err := os.Stat(filepath.Join(root, "ghost")); err == nil {
		t.Fatal("esc created the folder anyway")
	}
}

// TestDirPickerRevealsFromChild checks that opening the picker on a deep
// directory reveals the whole ancestor chain up to the filesystem root while still
// landing the cursor on that directory, so its parents are reachable by arrowing
// up without any re-root key.
func TestDirPickerRevealsFromChild(t *testing.T) {
	root := pickerTree(t)
	child := filepath.Join(root, "alpha")
	p := NewDirectoryPicker(testTheme(), child, "", child, nil, 60, 14)

	if want := fsRoot(child); p.root.path != want {
		t.Fatalf("root = %q, want filesystem root %q", p.root.path, want)
	}
	if cur, ok := p.currentPath(); !ok || cur != child {
		t.Errorf("cursor on %q, want the opened dir %q", cur, child)
	}
	if findNode(p, root) == nil {
		t.Errorf("parent %q not reachable in the revealed tree", root)
	}
}

// TestDirPickerFilterFindsNested types a query and checks that a deep match is
// surfaced with its ancestor chain and marked as a fuzzy hit.
func TestDirPickerFilterFindsNested(t *testing.T) {
	root := pickerTree(t)
	p := NewDirectoryPicker(testTheme(), root, "", root, nil, 60, 14)

	p, _ = p.Update(keyRunes("nested"))

	if !p.filtering {
		t.Fatal("expected picker to be filtering")
	}
	p = runFilter(t, p)

	nested := filepath.Join(root, "alpha", "nested")
	var hit *treeNode
	for _, r := range p.visible {
		if r.node.path == nested {
			hit = r.node
		}
	}
	if hit == nil {
		t.Fatalf(
			"filter did not surface %q; visible = %v",
			nested,
			visiblePaths(p),
		)
	}
	if !hit.matched {
		t.Errorf("%q should be marked as a fuzzy match", nested)
	}
	if p.visible[p.cursor].node != hit {
		t.Errorf("cursor should rest on the match")
	}
}

// TestDirPickerFilterEscClears checks that the first esc clears an active filter
// and returns to the browse tree rather than cancelling.
func TestDirPickerFilterEscClears(t *testing.T) {
	root := pickerTree(t)
	p := NewDirectoryPicker(testTheme(), root, "", root, nil, 60, 14)

	p, _ = p.Update(keyRunes("beta"))
	p, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEsc})

	if _, ok := runCmd(cmd).(DirCancelledMsg); ok {
		t.Fatal("first esc cancelled instead of clearing the filter")
	}
	if p.filtering {
		t.Errorf("filter should be cleared after esc")
	}
}

func TestDirPickerEscCancels(t *testing.T) {
	root := pickerTree(t)
	p := NewDirectoryPicker(testTheme(), root, "", root, nil, 60, 14)
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if _, ok := runCmd(cmd).(DirCancelledMsg); !ok {
		t.Errorf("esc emitted %T, want DirCancelledMsg", runCmd(cmd))
	}
}

func TestHomeRel(t *testing.T) {
	home := "/Users/joakim"
	cases := []struct{ path, want string }{
		{home, "~"},
		{filepath.Join(home, "Repos", "wasa"), "~/Repos/wasa"},
		{"/etc/hosts", "/etc/hosts"},
	}
	for _, c := range cases {
		if got := HomeRel(c.path, home); got != c.want {
			t.Errorf("HomeRel(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
}
