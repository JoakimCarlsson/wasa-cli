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

func TestNormalizeClaude(t *testing.T) {
	native := `{"type":"summary","summary":"x"}
{"type":"user","timestamp":"2026-07-08T10:00:00Z","message":{"role":"user","content":"add dark mode"}}
{"type":"assistant","timestamp":"2026-07-08T10:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}
`
	r := claudeRecorder{}
	checkMessages(t, r.Normalize([]byte(native)), []wantMsg{
		{"", "", false},
		{"user", "add dark mode", true},
		{"assistant", "done", true},
	})
	if got := r.Intent([]byte(native)); got != "add dark mode" {
		t.Errorf("Intent = %q", got)
	}
}

func TestNormalizeCodex(t *testing.T) {
	native := `{"timestamp":"2026-07-08T10:00:00Z","type":"session_meta","payload":{"id":"abc"}}
{"timestamp":"2026-07-08T10:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>ctx</environment_context>"},{"type":"input_text","text":"fix the bug"}]}}
{"timestamp":"2026-07-08T10:00:03Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fixed"}]}}
`
	r := codexRecorder{}
	checkMessages(t, r.Normalize([]byte(native)), []wantMsg{
		{"", "", true},
		{"user", "fix the bug", true},
		{"assistant", "fixed", true},
	})
	if got := r.Intent([]byte(native)); got != "fix the bug" {
		t.Errorf("Intent = %q (system context should be dropped)", got)
	}
}

func TestNormalizeCursor(t *testing.T) {
	native := `{"role":"user","message":{"content":"rename the field"}}
{"role":"assistant","message":{"content":[{"type":"text","text":"ok"}]}}
`
	r := cursorRecorder{}
	checkMessages(t, r.Normalize([]byte(native)), []wantMsg{
		{"user", "rename the field", false},
		{"assistant", "ok", false},
	})
	if got := r.Intent([]byte(native)); got != "rename the field" {
		t.Errorf("Intent = %q", got)
	}
}

func TestNormalizeCopilot(t *testing.T) {
	native := `{"type":"session.start","timestamp":1751968800000,"data":{}}
{"type":"user.message","timestamp":1751968800000,"data":{"content":"write tests"}}
{"type":"assistant.message","timestamp":"2026-07-08T10:00:05Z","data":{"content":"sure"}}
`
	r := copilotRecorder{}
	checkMessages(t, r.Normalize([]byte(native)), []wantMsg{
		{"", "", true},
		{"user", "write tests", true},
		{"assistant", "sure", true},
	})
	if got := r.Intent([]byte(native)); got != "write tests" {
		t.Errorf("Intent = %q", got)
	}
}

func TestNormalizeGemini(t *testing.T) {
	native := `{"sessionId":"g1","messages":[` +
		`{"type":"user","timestamp":"2026-07-08T10:00:00Z","content":"refactor"},` +
		`{"type":"gemini","timestamp":"2026-07-08T10:00:02Z","content":[{"text":"done"}]},` +
		`{"type":"info","content":"noise"}]}`
	r := geminiRecorder{}
	checkMessages(t, r.Normalize([]byte(native)), []wantMsg{
		{"user", "refactor", true},
		{"assistant", "done", true},
		{"", "", false},
	})
	if got := r.Intent([]byte(native)); got != "refactor" {
		t.Errorf("Intent = %q", got)
	}
}

// TestNormalizeRoundTrip verifies the raw field reconstructs the native
// transcript for the line-based agents, so a native resume finds the agent's
// own format after a checkpoint restore.
func TestNormalizeRoundTrip(t *testing.T) {
	cases := map[string]string{
		"claude": `{"type":"user","message":{"role":"user","content":"hi"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"yo"}]}}
`,
		"codex": `{"timestamp":"2026-07-08T10:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"go"}]}}
`,
		"cursor": `{"role":"user","message":{"content":"a"}}
{"role":"assistant","message":{"content":"b"}}
`,
		"copilot": `{"type":"user.message","timestamp":1751968800000,"data":{"content":"c"}}
`,
	}
	for tool, native := range cases {
		t.Run(tool, func(t *testing.T) {
			stored := normalize(tool, []byte(native))
			if !looksNormalized(stored) {
				t.Fatalf("normalized output not detected as normalized")
			}
			if got := string(denormalize(stored)); got != native {
				t.Errorf(
					"round-trip mismatch:\n got: %q\nwant: %q",
					got,
					native,
				)
			}
		})
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
