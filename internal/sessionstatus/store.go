package sessionstatus

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Freshness is how long a hook Record is trusted after its timestamp. Past it
// the cockpit falls back to the pane heuristic, so a crashed agent that stopped
// emitting events does not pin a session at a stale "waiting" forever.
const Freshness = 2 * time.Minute

// Record is the hook status persisted for one session: the reported Status, the
// raw event name that produced it (kept for debugging), and the time it was
// written. It is the message passed from the `wasa hook-handler` process to the
// cockpit process through a file under $WASA_HOME/hooks.
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
		return errors.New("sessionstatus: empty session id")
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
// when no record exists or it cannot be parsed; a malformed or non-activity
// record is treated as absent rather than an error, so the cockpit silently
// falls back to the pane heuristic.
func Read(home, sessionID string) (Record, bool) {
	data, err := os.ReadFile(recordPath(home, sessionID))
	if errors.Is(err, fs.ErrNotExist) || err != nil {
		return Record{}, false
	}
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return Record{}, false
	}
	if !r.Status.Activity() {
		return Record{}, false
	}
	return r, true
}

// Remove deletes the hook record for sessionID under home, if present. It is the
// cleanup counterpart to Write, run when a session is deleted. A missing record
// is not an error.
func Remove(home, sessionID string) error {
	err := os.Remove(recordPath(home, sessionID))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
