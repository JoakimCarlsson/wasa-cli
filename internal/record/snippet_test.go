package record

import (
	"strings"
	"testing"
)

func TestSnippet(t *testing.T) {
	line, s, e := Snippet("  the retry logic here", 6, 17, 100)
	if line != "the retry logic here" {
		t.Fatalf("short line not trimmed: %q", line)
	}
	if line[s:e] != "retry logic" {
		t.Fatalf("span %q, want %q", line[s:e], "retry logic")
	}

	long := strings.Repeat("x", 200) + "MATCH" + strings.Repeat("y", 200)
	out, hs, he := Snippet(long, 200, 205, 40)
	if out[hs:he] != "MATCH" {
		t.Fatalf("windowed span = %q, want MATCH", out[hs:he])
	}
	if !strings.HasPrefix(out, "…") || !strings.HasSuffix(out, "…") {
		t.Fatalf("expected ellipses on both ends: %q", out)
	}

	head := "MATCH" + strings.Repeat("z", 200)
	out, hs, he = Snippet(head, 0, 5, 40)
	if strings.HasPrefix(out, "…") {
		t.Fatalf("unexpected leading ellipsis: %q", out)
	}
	if out[hs:he] != "MATCH" {
		t.Fatalf("head span = %q, want MATCH", out[hs:he])
	}
}
