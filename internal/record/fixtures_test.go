package record

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// recorderSpec declares, for one supported recorder, the fixture file
// extension and the normalized-event properties its captured session fixture
// must satisfy. recorderSpecs is the single table the conformance test
// iterates, so every recorder is held to the same contract and teaching wasa a
// new agent is one row here plus its testdata/ directory.
type recorderSpec struct {
	// tool is the recorder's Tool() value and its testdata subdirectory.
	tool string
	// r is the recorder under test.
	r Recorder
	// ext is the fixture file extension: jsonl (line-based), json (Gemini's
	// single object), or md (Aider's chat log).
	ext string
	// lineBased is true when the transcript is one native line per message, so
	// denormalize(normalize(native)) must reconstruct native byte-for-byte.
	lineBased bool
	// hasTimestamps is true when the format carries a per-turn timestamp, so
	// every user/assistant turn must normalize with a non-zero Timestamp.
	hasTimestamps bool
	// wantIntent is the intent Intent() must extract from session.<ext>.
	wantIntent string
}

// cursorIntent is the intent wasa currently extracts from Cursor's session
// fixture. Cursor wraps the typed prompt in <timestamp>/<user_query> and wasa
// does not strip those, so the wrapper survives into the intent; this pins
// that behavior (see testdata/README.md).
const cursorIntent = "<timestamp>Sunday, July 5, 2026, 2:40 PM (UTC+2)" +
	"</timestamp>\n<user_query>\nRename the Field struct to Label across " +
	"the package.\n</user_query>"

// recorderSpecs is the conformance table: one row per supported recorder,
// matching the six agents that declare a RecorderTool in agent.Agents.
var recorderSpecs = []recorderSpec{
	{
		tool: "claude", r: claudeRecorder{}, ext: "jsonl",
		lineBased: true, hasTimestamps: true,
		wantIntent: "Add a greeting helper to the CLI and print it on startup.",
	},
	{
		tool: "codex", r: codexRecorder{}, ext: "jsonl",
		lineBased: true, hasTimestamps: true,
		wantIntent: "Fix the failing test in parser_test.go.",
	},
	{
		tool: "copilot", r: copilotRecorder{}, ext: "jsonl",
		lineBased: true, hasTimestamps: true,
		wantIntent: "Write a unit test for the greeting helper.",
	},
	{
		tool: "gemini", r: geminiRecorder{}, ext: "json",
		lineBased: false, hasTimestamps: true,
		wantIntent: "Add a README section about configuration.",
	},
	{
		tool: "cursor", r: cursorRecorder{}, ext: "jsonl",
		lineBased: false, hasTimestamps: false,
		wantIntent: cursorIntent,
	},
	{
		tool: "aider", r: aiderRecorder{}, ext: "md",
		lineBased: false, hasTimestamps: false,
		wantIntent: "Add a --version flag to the CLI.",
	},
}

// specFor returns the conformance spec for a tool, failing the test when none
// is declared so a new recorder without a fixture row is caught.
func specFor(t *testing.T, tool string) recorderSpec {
	t.Helper()
	for _, s := range recorderSpecs {
		if s.tool == tool {
			return s
		}
	}
	t.Fatalf("no recorderSpec declared for tool %q", tool)
	return recorderSpec{}
}

// readFixture reads a testdata fixture for tool, normalizing CRLF to LF so the
// line-based round-trip assertions are stable regardless of git's checkout
// line-ending policy — agents write their transcripts with LF.
func readFixture(t *testing.T, tool, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", tool, name))
	if err != nil {
		t.Fatalf("read fixture %s/%s: %v", tool, name, err)
	}
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}
