package cli

import (
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/joakimcarlsson/wasa/internal/agenthook"
	"github.com/joakimcarlsson/wasa/internal/hook"
	"github.com/joakimcarlsson/wasa/internal/hookstatus"
)

func init() {
	commands = append(commands, &Command{
		Name:    "hook-handler",
		Summary: "internal: record an agent lifecycle hook event",
		Hidden:  true,
		Run:     runHookHandler,
	})
}

// runHookHandler is invoked by a hook-emitting agent (currently Claude Code) on
// its lifecycle events. It reads the event payload on stdin, maps the event to a
// status and writes a per-session hook record the cockpit reads. It is
// fire-and-forget by contract: it ALWAYS reports success, never writing to
// stderr or returning a non-zero result, so a synchronous hook (Claude's Stop)
// can never block or disturb the agent. A missing session id, an unmappable
// event or a write failure are all silently no-ops — the cockpit just falls
// back to the pane heuristic.
func runHookHandler(args []string) error {
	sessionID := os.Getenv(hook.EnvSession)
	if sessionID == "" {
		return nil
	}

	data, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		return nil
	}
	var payload struct {
		Event string `json:"hook_event_name"`
	}
	_ = json.Unmarshal(data, &payload)

	status, ok := agenthook.MapEvent(payload.Event)
	if !ok {
		return nil
	}

	home := os.Getenv("WASA_HOME")
	if home == "" {
		home = wasaHome()
	}
	_ = hookstatus.Write(home, sessionID, hookstatus.Record{
		Status:    status,
		Event:     payload.Event,
		UpdatedAt: time.Now(),
	})
	return nil
}
