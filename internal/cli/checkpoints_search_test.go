package cli

import (
	"strings"
	"testing"
)

func TestPartitionArgs(t *testing.T) {
	takes := map[string]bool{"branch": true, "since": true, "limit": true}
	flags, pos := partitionArgs(
		[]string{"retry logic", "--intent-only", "--branch", "task/x", "--limit=5"},
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

func TestMakeSnippet(t *testing.T) {
	line, s, e := makeSnippet("  the retry logic here", 6, 17, 100)
	if line != "the retry logic here" {
		t.Fatalf("short line not trimmed: %q", line)
	}
	if line[s:e] != "retry logic" {
		t.Fatalf("span %q, want %q", line[s:e], "retry logic")
	}

	long := strings.Repeat("x", 200) + "MATCH" + strings.Repeat("y", 200)
	out, hs, he := makeSnippet(long, 200, 205, 40)
	if out[hs:he] != "MATCH" {
		t.Fatalf("windowed span = %q, want MATCH", out[hs:he])
	}
	if !strings.HasPrefix(out, "…") || !strings.HasSuffix(out, "…") {
		t.Fatalf("expected ellipses on both ends: %q", out)
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
