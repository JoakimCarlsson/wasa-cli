package cli

import (
	"strings"
	"testing"
)

func TestPartitionArgs(t *testing.T) {
	takes := map[string]bool{"branch": true, "since": true, "limit": true}
	flags, pos := partitionArgs(
		[]string{
			"retry logic",
			"--intent-only",
			"--branch",
			"task/x",
			"--limit=5",
		},
		takes,
	)
	if len(pos) != 1 || pos[0] != "retry logic" {
		t.Fatalf("positional = %v, want [retry logic]", pos)
	}
	want := "--intent-only --branch task/x --limit=5"
	if got := strings.Join(flags, " "); got != want {
		t.Fatalf("flags = %q, want %q", got, want)
	}
}

func TestHighlight(t *testing.T) {
	if got := highlight("abc", 0, 1, false); got != "abc" {
		t.Fatalf("no-color highlight = %q, want abc", got)
	}
	got := highlight("abc", 0, 1, true)
	if !strings.Contains(got, "\x1b[1;33m") || !strings.HasSuffix(got, "bc") {
		t.Fatalf("colored highlight = %q", got)
	}
}
