package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

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
func visiblePaths(p dirPicker) []string {
	out := make([]string, len(p.visible))
	for i, r := range p.visible {
		out[i] = r.node.path
	}
	return out
}

func TestNewDirPickerListsTopLevelSkippingNoise(t *testing.T) {
	root := pickerTree(t)
	p := newDirPicker(root, "", root, nil, 60, 14)

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
	p := newDirPicker(root, "", root, nil, 60, 14)

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
	p := newDirPicker(root, beta, root, nil, 60, 14)

	if p.visible[p.cursor].node.path != beta {
		t.Errorf("cursor on %q, want %q", p.visible[p.cursor].node.path, beta)
	}
}

func keyDown() tea.KeyMsg  { return tea.KeyMsg{Type: tea.KeyDown} }
func keyRight() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRight} }
func keyEnter() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} }

// runFilter drives the picker's debounced async filter to completion: it runs
// the deferred walk for the current generation and applies the result, the same
// sequence the model performs in response to the tick and result messages.
func runFilter(t *testing.T, p dirPicker) dirPicker {
	t.Helper()
	cmd := (&p).tickFilter(p.filterGen)
	if cmd == nil {
		t.Fatal("no filter walk was scheduled")
	}
	msg, ok := cmd().(filterResultMsg)
	if !ok {
		t.Fatalf("filter walk produced %T, want filterResultMsg", cmd())
	}
	(&p).applyFilterResult(msg)
	return p
}

func TestDirPickerExpandRevealsChildren(t *testing.T) {
	root := pickerTree(t)
	p := newDirPicker(root, "", root, nil, 60, 14)

	p, _, _ = p.update(keyDown())  // onto alpha
	p, _, _ = p.update(keyRight()) // expand alpha

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
	p := newDirPicker(root, "", root, nil, 60, 14)

	p, _, _ = p.update(keyDown())  // alpha
	p, _, _ = p.update(keyRight()) // expand
	expanded := len(p.visible)
	p, _, _ = p.update(keyRight()) // collapse

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
	p := newDirPicker(root, "", root, nil, 60, 14)

	p, _, _ = p.update(keyDown()) // alpha
	p, result, _ := p.update(keyEnter())

	if result != pickChoose {
		t.Fatalf("result = %v, want pickChoose", result)
	}
	if want := filepath.Join(root, "alpha"); p.chosen != want {
		t.Errorf("chosen = %q, want %q", p.chosen, want)
	}
}

func TestDirPickerAscendRoot(t *testing.T) {
	root := pickerTree(t)
	child := filepath.Join(root, "alpha")
	p := newDirPicker(child, "", child, nil, 60, 14)

	p, _, _ = p.update(keyRunes("-"))

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
	p := newDirPicker(root, "", root, nil, 60, 14)

	p, _, _ = p.update(keyRunes("nested"))

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
	p := newDirPicker(root, "", root, nil, 60, 14)

	p, _, _ = p.update(keyRunes("beta"))
	p, result, _ := p.update(tea.KeyMsg{Type: tea.KeyEsc})

	if result != pickNone {
		t.Fatalf("first esc result = %v, want pickNone (clear)", result)
	}
	if p.filtering {
		t.Errorf("filter should be cleared after esc")
	}
}

func TestDirPickerEscCancels(t *testing.T) {
	root := pickerTree(t)
	p := newDirPicker(root, "", root, nil, 60, 14)
	_, result, _ := p.update(tea.KeyMsg{Type: tea.KeyEsc})
	if result != pickCancel {
		t.Errorf("result = %v, want pickCancel", result)
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
		if got := homeRel(c.path, home); got != c.want {
			t.Errorf("homeRel(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
}
