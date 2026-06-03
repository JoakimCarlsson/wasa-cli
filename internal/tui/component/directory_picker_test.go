package component

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/joakimcarlsson/wasa/internal/config"
	"github.com/joakimcarlsson/wasa/internal/tui/theme"
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
	root := t.TempDir()
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

func keyRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// visiblePaths is the on-screen node paths in display order.
func visiblePaths(p DirectoryPicker) []string {
	out := make([]string, len(p.visible))
	for i, r := range p.visible {
		out[i] = r.node.path
	}
	return out
}

func TestNewDirPickerListsTopLevelSkippingNoise(t *testing.T) {
	root := pickerTree(t)
	p := NewDirectoryPicker(testTheme(), root, "", root, nil, 60, 14)

	got := visiblePaths(p)
	want := []string{
		root,
		filepath.Join(root, "alpha"),
		filepath.Join(root, "beta"),
	}
	if len(got) != len(want) {
		t.Fatalf(
			"visible = %v, want %v (hidden/cache/files skipped)",
			got,
			want,
		)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("visible[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNewDirPickerMarksRepo(t *testing.T) {
	root := pickerTree(t)
	p := NewDirectoryPicker(testTheme(), root, "", root, nil, 60, 14)

	alpha := p.visible[1].node
	if alpha.name != "alpha" || !alpha.isRepo {
		t.Errorf("alpha.isRepo = %v, want true", alpha.isRepo)
	}
	if beta := p.visible[2].node; beta.isRepo {
		t.Errorf("beta.isRepo = true, want false")
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

func keyDown() tea.KeyMsg  { return tea.KeyMsg{Type: tea.KeyDown} }
func keyRight() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRight} }
func keyEnter() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} }

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

	p, _ = p.Update(keyDown())  // onto alpha
	p, _ = p.Update(keyRight()) // expand alpha

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

	p, _ = p.Update(keyDown())  // alpha
	p, _ = p.Update(keyRight()) // expand
	expanded := len(p.visible)
	p, _ = p.Update(keyRight()) // collapse

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

	p, _ = p.Update(keyDown()) // alpha
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
	p, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEsc})

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

func TestDirPickerAscendRoot(t *testing.T) {
	root := pickerTree(t)
	child := filepath.Join(root, "alpha")
	p := NewDirectoryPicker(testTheme(), child, "", child, nil, 60, 14)

	p, _ = p.Update(keyRunes("-"))

	if p.root.path != root {
		t.Fatalf("root = %q, want %q", p.root.path, root)
	}
	if p.visible[p.cursor].node.path != child {
		t.Errorf(
			"cursor on %q, want former root %q",
			p.visible[p.cursor].node.path,
			child,
		)
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
	p, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEsc})

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
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEsc})
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
