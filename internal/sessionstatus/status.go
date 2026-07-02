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
