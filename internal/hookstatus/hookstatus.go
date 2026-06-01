// Package hookstatus is the runtime channel through which a hook-emitting agent
// (Claude Code, and later Gemini/Codex/OpenCode/Copilot/Cursor) reports its own
// lifecycle state to the wasa cockpit. The agent invokes `wasa hook-handler` on
// its lifecycle events; that handler maps the event to a Status and writes a
// small per-session record under $WASA_HOME/hooks. The cockpit reads those
// records and prefers a fresh one over the pane-content heuristic, so for tools
// that have a real status API wasa uses the API rather than scraping the
// terminal.
//
// This is deliberately the same shape as agent-deck's hook channel: an
// authoritative signal for hook-capable tools, layered over a best-effort pane
// heuristic that remains the only option for plain shells and arbitrary
// programs. The store itself is tool-agnostic — it knows nothing about which
// events a given agent emits; the event→status mapping lives with each tool's
// adapter. Records are runtime state, never part of the persisted registry, so a
// stale or malformed record can only mislabel a dot.
package hookstatus

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Status is a session's hook-reported lifecycle state. It is intentionally the
// same vocabulary the cockpit renders, minus exited (liveness stays the
// registry's job): working while the agent is producing, waiting when it needs
// the user, idle when it has finished a turn and is quiet.
type Status string

const (
	StatusWorking Status = "working"
	StatusWaiting Status = "waiting"
	StatusIdle    Status = "idle"
)

// Valid reports whether s is a status the store recognises, so a handler cannot
// persist a typo that the cockpit would later have to defend against.
func (s Status) Valid() bool {
	switch s {
	case StatusWorking, StatusWaiting, StatusIdle:
		return true
	default:
		return false
	}
}

// Freshness is how long a record is trusted after its timestamp. Past it the
// cockpit falls back to the pane heuristic, so a crashed agent that stopped
// emitting events does not pin a session at a stale "waiting" forever. It
// mirrors agent-deck's two-minute hook fast-path window.
const Freshness = 2 * time.Minute

// Record is the hook status persisted for one session: the derived Status, the
// raw event name that produced it (kept for debugging and future per-tool
// nuance), and the time it was written.
type Record struct {
	Status    Status    `json:"status"`
	Event     string    `json:"event"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Fresh reports whether the record is recent enough to trust at time now.
func (r Record) Fresh(now time.Time) bool {
	return !r.UpdatedAt.IsZero() && now.Sub(r.UpdatedAt) <= Freshness
}

// Dir is the directory holding hook records under a $WASA_HOME.
func Dir(home string) string { return filepath.Join(home, "hooks") }

func recordPath(home, sessionID string) string {
	return filepath.Join(Dir(home), sessionID+".json")
}

// Write atomically stores r as the hook record for sessionID under home. It
// writes a temp file in the same directory and renames it into place so a
// concurrent reader never observes a half-written record.
func Write(home, sessionID string, r Record) error {
	if sessionID == "" {
		return errors.New("hookstatus: empty session id")
	}
	dir := Dir(home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, sessionID+".json.tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, recordPath(home, sessionID))
}

// Read returns the hook record for sessionID under home. The boolean is false
// when no record exists or it cannot be parsed; a malformed record is treated
// as absent rather than an error, so the cockpit silently falls back to the
// pane heuristic.
func Read(home, sessionID string) (Record, bool) {
	data, err := os.ReadFile(recordPath(home, sessionID))
	if errors.Is(err, fs.ErrNotExist) || err != nil {
		return Record{}, false
	}
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return Record{}, false
	}
	if !r.Status.Valid() {
		return Record{}, false
	}
	return r, true
}

// Remove deletes the hook record for sessionID under home, if present. It is
// the cleanup counterpart to Write, run when a session is deleted. A missing
// record is not an error.
func Remove(home, sessionID string) error {
	err := os.Remove(recordPath(home, sessionID))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
