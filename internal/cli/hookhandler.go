package cli

import (
	"encoding/json"
	"flag"
	"io"
	"os"
	"time"

	"github.com/joakimcarlsson/wasa/internal/hook"
	"github.com/joakimcarlsson/wasa/internal/sessionstatus"
)

func init() {
	commands = append(commands, &Command{
		Name:    "hook-handler",
		Summary: "internal: record an agent lifecycle hook event",
		Hidden:  true,
		Run:     runHookHandler,
	})
}

// runHookHandler is invoked by a hook-emitting agent on its lifecycle events,
// told which agent it serves via --tool. It reads the event payload on stdin,
// maps it through that agent's adapter and writes a per-session record the
// cockpit reads. It is fire-and-forget by contract: it ALWAYS reports success,
// never writing to stderr or returning non-zero, so a synchronous hook (Claude's
// Stop) can never block or disturb the agent. A missing session id, unknown
// tool, unmappable event or write failure are all silently no-ops — the cockpit
// just falls back to the pane heuristic.
func runHookHandler(args []string) error {
	fs := flag.NewFlagSet("hook-handler", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	tool := fs.String("tool", "", "agent whose event vocabulary to use")
	if err := fs.Parse(args); err != nil {
		return nil
	}

	sessionID := os.Getenv(hook.EnvSession)
	if sessionID == "" {
		return nil
	}
	adapter, ok := sessionstatus.Lookup(*tool)
	if !ok {
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

	status, ok := adapter.MapEvent(payload.Event)
	if !ok {
		return nil
	}

	home := os.Getenv("WASA_HOME")
	if home == "" {
		home = wasaHome()
	}
	_ = sessionstatus.Write(home, sessionID, sessionstatus.Record{
		Status:    status,
		Event:     payload.Event,
		UpdatedAt: time.Now(),
	})
	return nil
}
