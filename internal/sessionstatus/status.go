// Package sessionstatus is the cockpit's per-session activity status: what state
// each running session is in (working, waiting for input, idle) and how that is
// determined. It owns the full status vocabulary plus the two ways the cockpit
// learns a status — the authoritative hook channel and the best-effort pane
// heuristic — and the rule for choosing between them.
//
// Two sources feed it. A hook-emitting agent (Claude, Gemini, Codex, Copilot,
// Cursor) reports its own lifecycle via `wasa hook-handler`, which maps
// the event through that agent's Adapter and writes a Record to the store; the
// cockpit reads it. Any other program — a plain shell, an arbitrary command —
// has no such channel, so its status is derived from the pane content by the
// heuristic. Derive picks the authoritative hook record when one is fresh and
// falls back to the heuristic otherwise.
//
// Nothing here is persisted to the registry: liveness (running/exited) remains
// the registry's job, and these activity states are runtime-only, so a stale or
// wrong reading can mislabel a status dot but never corrupt state.
package sessionstatus

// Status is a session's activity state. Working/Waiting/Idle are the activity
// states a hook or the heuristic can produce; Exited reflects registry liveness
// and Unknown is a running session not yet observed. The zero value is Unknown.
type Status string

// The activity states, plus the two the cockpit assigns directly: Exited from
// registry liveness and Unknown (the zero value) for a session not yet observed.
const (
	Unknown Status = ""
	Working Status = "working"
	Waiting Status = "waiting"
	Idle    Status = "idle"
	Exited  Status = "exited"
)

// Activity reports whether s is one of the states a hook or the heuristic may
// produce, so a handler or store cannot persist Exited/Unknown, which are the
// cockpit's to assign.
func (s Status) Activity() bool {
	switch s {
	case Working, Waiting, Idle:
		return true
	default:
		return false
	}
}

// Label is the short word the cockpit renders for the status. Unknown renders as
// "running" — a session is known to be alive, just not yet classified.
func (s Status) Label() string {
	switch s {
	case Working:
		return "working"
	case Waiting:
		return "waiting"
	case Idle:
		return "idle"
	case Exited:
		return "exited"
	default:
		return "running"
	}
}
