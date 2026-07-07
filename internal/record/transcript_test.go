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

func TestSanitizeIntent(t *testing.T) {
	cases := []struct{ in, want string }{
		{"just a prompt", "just a prompt"},
		{
			"<ide_selection>foo.go:12</ide_selection>fix the parser",
			"fix the parser",
		},
		{
			"add a cache\n<system-reminder>be lazy</system-reminder>",
			"add a cache",
		},
		{
			"<context>repo state</context>\n<ide-opened-file>x</ide-opened-file>" +
				"do the thing",
			"do the thing",
		},
	}
	for _, c := range cases {
		if got := sanitizeIntent(c.in); got != c.want {
			t.Errorf("sanitizeIntent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFirstUserMessageStripsWrappers(t *testing.T) {
	transcript := `{"type":"user","message":{"role":"user","content":` +
		`"<ide_selection>a.go:1</ide_selection>rename the field"}}`
	if got := FirstUserMessage([]byte(transcript)); got != "rename the field" {
		t.Errorf("FirstUserMessage = %q, want %q", got, "rename the field")
	}
}
