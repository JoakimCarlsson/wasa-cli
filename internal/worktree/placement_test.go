package worktree

import (
	"path/filepath"
	"testing"
)

func TestSanitizeBranch(t *testing.T) {
	cases := []struct {
		name   string
		branch string
		want   string
	}{
		{"plain", "main", "main"},
		{"slash", "feature/x", "feature-x"},
		{"nested slashes", "feature/foo/bar", "feature-foo-bar"},
		{"dotted version", "release/v1.2.3", "release-v1.2.3"},
		{"backslash and colon", `wip\thing:2`, "wip-thing-2"},
		{"unsafe glob chars", "fix?*<>|", "fix"},
		{"collapse runs", "a///b", "a-b"},
		{"trim edges", "/leading/trailing/", "leading-trailing"},
		{"underscores kept", "feature_x", "feature_x"},
		{"empty becomes fallback", "///", "branch"},
		{"only dots becomes fallback", "...", "branch"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeBranch(tc.branch); got != tc.want {
				t.Fatalf(
					"sanitizeBranch(%q) = %q, want %q",
					tc.branch,
					got,
					tc.want,
				)
			}
		})
	}
}

func TestCentralLayout(t *testing.T) {
	got := Central(Params{
		Home:      filepath.Join("home", ".wasa"),
		Workspace: "wasa",
		Branch:    "feature/demo",
	})
	want := filepath.Join("home", ".wasa", "worktrees", "wasa", "feature-demo")
	if got != want {
		t.Fatalf("Central path = %q, want %q", got, want)
	}
}

func TestCentralLayoutSanitizesBranchSegment(t *testing.T) {
	got := Central(Params{
		Home:      "root",
		Workspace: "ws",
		Branch:    "a/b/c",
	})
	want := filepath.Join("root", "worktrees", "ws", "a-b-c")
	if got != want {
		t.Fatalf("Central path = %q, want %q", got, want)
	}
}

func TestManagerPathRoutesThroughLayout(t *testing.T) {
	var seen Params
	m := &Manager{
		Home:      "h",
		Workspace: "w",
		RepoDir:   "/repo",
		Layout: func(p Params) string {
			seen = p
			return "sentinel"
		},
	}

	if got := m.Path("feature/x"); got != "sentinel" {
		t.Fatalf("Path did not route through Layout: got %q", got)
	}
	if seen.Branch != "feature/x" || seen.Home != "h" ||
		seen.Workspace != "w" || seen.RepoPath != "/repo" {
		t.Fatalf("Layout received unexpected Params: %+v", seen)
	}
}

func TestManagerPathDefaultsToCentral(t *testing.T) {
	m := &Manager{Home: "h", Workspace: "w"}
	want := filepath.Join("h", "worktrees", "w", "main")
	if got := m.Path("main"); got != want {
		t.Fatalf("Path with nil Layout = %q, want %q", got, want)
	}
}
