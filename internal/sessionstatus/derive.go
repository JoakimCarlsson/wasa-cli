package sessionstatus

import "time"

// Derive is the cockpit's authority rule: it returns the activity status to
// trust for a running session, preferring a fresh hook Record — the
// authoritative signal from a hook-emitting agent — and falling back to scraped,
// the heuristic status derived from pane content, when no fresh record exists.
//
// This is the seam that makes wasa use a tool's real status API when it has one
// and scrape the terminal only when it must.
func Derive(home, sessionID string, scraped Status, now time.Time) Status {
	if rec, ok := Read(home, sessionID); ok && rec.Fresh(now) {
		return rec.Status
	}
	return scraped
}
