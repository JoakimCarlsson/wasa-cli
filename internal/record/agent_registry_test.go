package record

import (
	"testing"

	"github.com/joakimcarlsson/wasa-cli/internal/agent"
)

// TestRecordersMatchAgentRegistry cross-checks the recorders slice against
// the canonical agent.Agents registry: every recorder's Exe/Tool pair must
// match a declared agent, and every agent that declares a RecorderTool must
// resolve to an implementation. This is the parity test that fails CI when a
// future agent is registered on one side (e.g. added to agent.Agents with a
// RecorderTool) but not the other, instead of silently shipping a half-wired
// agent.
func TestRecordersMatchAgentRegistry(t *testing.T) {
	for _, r := range recorders {
		a, ok := agent.ByExe(r.Exe())
		if !ok {
			t.Errorf(
				"recorder %q: exe %q has no declared agent in agent.Agents",
				r.Tool(), r.Exe(),
			)
			continue
		}
		if a.RecorderTool != r.Tool() {
			t.Errorf(
				"agent %q declares RecorderTool %q, but its recorder's Tool() = %q",
				a.Exe, a.RecorderTool, r.Tool(),
			)
		}
	}

	for _, a := range agent.Agents {
		if a.RecorderTool == "" {
			continue
		}
		r, ok := recorderFor(a.RecorderTool)
		if !ok {
			t.Errorf(
				"agent %q declares RecorderTool %q with no matching recorder",
				a.Exe, a.RecorderTool,
			)
			continue
		}
		if r.Exe() != a.Exe {
			t.Errorf(
				"recorder for tool %q has Exe() = %q, want %q (agent's declared Exe)",
				a.RecorderTool, r.Exe(), a.Exe,
			)
		}
	}
}
