package record

import "testing"

func TestFirstUserMessage(t *testing.T) {
	transcript := `{"type":"summary","summary":"stuff"}
{"type":"user","isMeta":true,"message":{"role":"user","content":"meta"}}
{"type":"user","message":{"role":"user","content":"<command-name>/init</command-name>"}}
{"type":"user","message":{"role":"user","content":"add dark mode"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}
{"type":"user","message":{"role":"user","content":"second prompt"}}`
	if got := FirstUserMessage([]byte(transcript)); got != "add dark mode" {
		t.Errorf("FirstUserMessage = %q, want %q", got, "add dark mode")
	}
}

func TestFirstUserMessageBlockContent(t *testing.T) {
	transcript := `{"type":"user","message":{"role":"user","content":[` +
		`{"type":"tool_result","content":"out"},` +
		`{"type":"text","text":"fix the flaky test"}]}}`
	if got := FirstUserMessage(
		[]byte(transcript),
	); got != "fix the flaky test" {
		t.Errorf("FirstUserMessage = %q", got)
	}
}

func TestFirstUserMessageEmptyAndGarbage(t *testing.T) {
	for _, in := range []string{"", "not json\n{broken", `{"type":"assistant"}`} {
		if got := FirstUserMessage([]byte(in)); got != "" {
			t.Errorf("FirstUserMessage(%q) = %q, want empty", in, got)
		}
	}
}
