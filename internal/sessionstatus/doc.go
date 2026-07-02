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
