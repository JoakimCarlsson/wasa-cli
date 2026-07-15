package cli

import (
	"strings"
	"testing"
)

func TestPushPullCommandsRegistered(t *testing.T) {
	for _, name := range []string{"push", "pull"} {
		if _, ok := lookup(name); !ok {
			t.Fatalf("%s command not registered", name)
		}
	}
}

func TestSyncArgsDefaultsToOrigin(t *testing.T) {
	remote, err := syncArgs(pushUsage, nil)
	if err != nil {
		t.Fatalf("syncArgs(nil) err = %v", err)
	}
	if remote != "origin" {
		t.Fatalf("remote = %q, want %q", remote, "origin")
	}
}

func TestSyncArgsExplicitRemote(t *testing.T) {
	remote, err := syncArgs(pullUsage, []string{"upstream"})
	if err != nil {
		t.Fatalf("syncArgs err = %v", err)
	}
	if remote != "upstream" {
		t.Fatalf("remote = %q, want %q", remote, "upstream")
	}
}

func TestSyncArgsTooManyIsError(t *testing.T) {
	if _, err := syncArgs(pushUsage, []string{"a", "b"}); err == nil ||
		!strings.Contains(err.Error(), "usage:") {
		t.Fatalf("syncArgs with two args err = %v, want usage error", err)
	}
}
