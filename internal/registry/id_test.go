package registry

import (
	"strings"
	"testing"
)

func TestWorkspaceIDIsStable(t *testing.T) {
	a := WorkspaceID("/home/me/repo", "git@github.com:me/repo.git")
	b := WorkspaceID("/home/me/repo", "git@github.com:me/repo.git")
	if a != b {
		t.Fatalf("WorkspaceID not stable: %q != %q", a, b)
	}
	if len(a) != idLen {
		t.Fatalf("WorkspaceID length = %d, want %d", len(a), idLen)
	}
}

func TestWorkspaceIDDiffersByPathAndRemote(t *testing.T) {
	base := WorkspaceID("/home/me/repo", "git@github.com:me/repo.git")

	if got := WorkspaceID("/home/me/other", "git@github.com:me/repo.git"); got == base {
		t.Fatal("WorkspaceID should differ for a different repo path")
	}
	if got := WorkspaceID("/home/me/repo", "git@github.com:me/fork.git"); got == base {
		t.Fatal("WorkspaceID should differ for a different remote")
	}
}

func TestWorkspaceIDStableWithoutRemote(t *testing.T) {
	a := WorkspaceID("/home/me/repo", "")
	b := WorkspaceID("/home/me/repo", "")
	if a != b {
		t.Fatalf("WorkspaceID without remote not stable: %q != %q", a, b)
	}
	if a == WorkspaceID("/home/me/other", "") {
		t.Fatal("remoteless WorkspaceID should still differ by path")
	}
}

func TestTmuxName(t *testing.T) {
	name := TmuxName("0123456789ab", "abcdef012345")
	if want := "wasa_01234567_abcdef01"; name != want {
		t.Fatalf("TmuxName = %q, want %q", name, want)
	}
	if strings.ContainsAny(name, ":.") {
		t.Fatalf("TmuxName %q contains a tmux target separator", name)
	}
}
