package cli

import (
	"encoding/json"
	"flag"
	"io"
	"os"

	"github.com/joakimcarlsson/wasa-cli/internal/hook"
	"github.com/joakimcarlsson/wasa-cli/internal/record"
)

func init() {
	commands = append(commands, &Command{
		Name:    "record-hook",
		Summary: "internal: record an agent session event as a checkpoint",
		Hidden:  true,
		Run:     runRecordHook,
	})
}

// runRecordHook is invoked by a recording hook on the agent's lifecycle
// events, told which agent it serves via --tool. It reads the event payload
// on stdin and hands it to the recorder, which tracks the transcript, writes
// commit-linked checkpoints and closes unmanaged sessions. Like hook-handler
// it is fire-and-forget by contract: it ALWAYS reports success, never
// writing to stderr or returning non-zero, so it can never block or disturb
// the agent. Any missing field or recorder failure is a silent no-op.
func runRecordHook(args []string) error {
	fs := flag.NewFlagSet("record-hook", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	tool := fs.String("tool", "", "agent whose event payload to read")
	if err := fs.Parse(args); err != nil {
		return nil
	}
	if *tool != "claude" {
		return nil
	}

	data, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		return nil
	}
	var payload struct {
		Event          string `json:"hook_event_name"`
		SessionID      string `json:"session_id"`
		TranscriptPath string `json:"transcript_path"`
		CWD            string `json:"cwd"`
	}
	_ = json.Unmarshal(data, &payload)
	if payload.CWD == "" {
		payload.CWD, _ = os.Getwd()
	}

	record.HandleEvent(wasaHome(), record.Event{
		Name:           payload.Event,
		Agent:          *tool,
		AgentSessionID: payload.SessionID,
		TranscriptPath: payload.TranscriptPath,
		Dir:            payload.CWD,
		WasaSession:    os.Getenv(hook.EnvSession),
	})
	return nil
}
