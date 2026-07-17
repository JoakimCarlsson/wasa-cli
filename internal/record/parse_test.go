package record

import "testing"

// The parse tests below read each recorder's captured session fixture and
// assert the exact normalized turn sequence — the golden expectation that
// documents how one real transcript maps to wasa's Message shape. They replace
// the former inline-string tests; the cross-recorder invariants (roles, raw,
// round-trip, intent, timestamps) live in conformance_test.go.

func TestParseClaude(t *testing.T) {
	native := readFixture(t, "claude", "session.jsonl")
	checkMessages(t, claudeRecorder{}.Normalize(native), []wantMsg{
		{"", "", false},
		{"", "", false},
		{"", "", false},
		{"", "", false},
		{
			"user",
			"Add a greeting helper to the CLI and print it on startup.",
			true,
		},
		{"", "", true},
		{
			"assistant",
			"I'll add a greeting helper and wire it into startup.",
			true,
		},
		{"assistant", "", true},
		{"user", "", true},
		{
			"assistant",
			"Done. greeting.txt now holds the greeting and it prints on startup.",
			true,
		},
		{"", "", true},
	})
}

func TestParseCodex(t *testing.T) {
	native := readFixture(t, "codex", "session.jsonl")
	checkMessages(t, codexRecorder{}.Normalize(native), []wantMsg{
		{"", "", true},
		{"user", "", true},
		{"user", "Fix the failing test in parser_test.go.", true},
		{"", "", true},
		{"", "", true},
		{"", "", true},
		{
			"assistant",
			"Fixed the off-by-one in the parser; the test passes now.",
			true,
		},
		{"", "", true},
	})
}

func TestParseCopilot(t *testing.T) {
	native := readFixture(t, "copilot", "session.jsonl")
	checkMessages(t, copilotRecorder{}.Normalize(native), []wantMsg{
		{"", "", true},
		{"", "", true},
		{"user", "Write a unit test for the greeting helper.", true},
		{"assistant", "I'll add a test that checks the greeting output.", true},
		{"", "", true},
		{"", "", true},
		{
			"assistant",
			"Added greeting_test.go covering the default greeting.",
			true,
		},
		{"", "", true},
	})
}

// TestParseCopilotEpoch covers Copilot's alternate epoch-millis timestamp
// encoding, which parseEpochOrRFC3339 must read as well as the RFC3339 form in
// session.jsonl.
func TestParseCopilotEpoch(t *testing.T) {
	native := readFixture(t, "copilot", "epoch.jsonl")
	checkMessages(t, copilotRecorder{}.Normalize(native), []wantMsg{
		{"", "", true},
		{"user", "epoch-millis timestamp variant", true},
		{"assistant", "handled the epoch-millis timestamp", true},
	})
}

func TestParseGemini(t *testing.T) {
	native := readFixture(t, "gemini", "session.json")
	checkMessages(t, geminiRecorder{}.Normalize(native), []wantMsg{
		{"user", "Add a README section about configuration.", true},
		{"", "", true},
		{"assistant", "Added a Configuration section to the README.", true},
	})
}

func TestParseCursor(t *testing.T) {
	native := readFixture(t, "cursor", "session.jsonl")
	checkMessages(t, cursorRecorder{}.Normalize(native), []wantMsg{
		{"user", cursorIntent, false},
		{
			"assistant",
			"I'll rename Field to Label and update the references.",
			false,
		},
		{
			"assistant",
			"Renamed Field to Label and updated all references.",
			false,
		},
	})
}

// TestParseCursorFallbackShapes covers the alternate content layouts
// cursorLine defends against beyond the block-array session fixture: a
// plain-string message.content, a top-level content field, and role taken from
// "type" when "role" is absent.
func TestParseCursorFallbackShapes(t *testing.T) {
	native := `{"role":"user","message":{"content":"rename the field"}}
{"type":"assistant","content":[{"type":"text","text":"done"}]}
`
	checkMessages(t, cursorRecorder{}.Normalize([]byte(native)), []wantMsg{
		{"user", "rename the field", false},
		{"assistant", "done", false},
	})
}
