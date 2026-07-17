package record

import (
	"strings"
	"testing"
)

// wantMsg is one expected normalized turn: only the fields a test asserts on.
type wantMsg struct {
	role    string
	content string
	hasTS   bool
}

func checkMessages(t *testing.T, got []Message, want []wantMsg) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d messages, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Role != w.role || got[i].Content != w.content {
			t.Errorf("msg %d = {%q,%q}, want {%q,%q}",
				i, got[i].Role, got[i].Content, w.role, w.content)
		}
		if got[i].Timestamp.IsZero() == w.hasTS {
			t.Errorf("msg %d timestamp zero=%v, want hasTS=%v",
				i, got[i].Timestamp.IsZero(), w.hasTS)
		}
		if len(got[i].Raw) == 0 {
			t.Errorf("msg %d has no raw payload", i)
		}
	}
}

func TestNormalizeUnknownAgentPassthrough(t *testing.T) {
	native := []byte(`{"whatever":true}` + "\n")
	if got := normalize(
		"no-such-agent",
		native,
	); string(
		got,
	) != string(
		native,
	) {
		t.Errorf("unknown agent should pass native through, got %q", got)
	}
	if looksNormalized(native) {
		t.Error("raw native detected as normalized")
	}
	if got := string(denormalize(native)); got != string(native) {
		t.Errorf("denormalize of raw native changed it: %q", got)
	}
}

func TestRenderTranscript(t *testing.T) {
	native := `{"type":"user","timestamp":"2026-07-08T10:00:00Z","message":{"role":"user","content":"add dark mode"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}
`
	rendered := RenderTranscript(normalize("claude", []byte(native)))
	for _, want := range []string{"user", "add dark mode", "assistant", "done"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered transcript missing %q:\n%s", want, rendered)
		}
	}
	if got := RenderTranscript([]byte(native)); got != native {
		t.Errorf("raw transcript should render unchanged, got:\n%s", got)
	}
}
