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

// runRecordHook is invoked by a recording hook on an agent's lifecycle
// events, told which agent it serves via --tool and whether this is the
// agent's session-end event via --event end (stamped on that entry at
// install time, so no per-agent event vocabulary is needed here). It reads
// the event payload on stdin — tolerating each agent's field spelling — and
// hands it to the recorder, which tracks the transcript, writes
// commit-linked checkpoints and closes unmanaged sessions. Like hook-handler
// it is fire-and-forget by contract: it ALWAYS reports success, never
// writing to stderr or returning non-zero, so it can never block or disturb
// the agent. Any missing field or recorder failure is a silent no-op.
func runRecordHook(args []string) error {
	fs := flag.NewFlagSet("record-hook", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	tool := fs.String("tool", "", "agent whose event payload to read")
	event := fs.String("event", "", "set to \"end\" on the session-end hook")
	if err := fs.Parse(args); err != nil {
		return nil
	}

	data, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		return nil
	}
	var p struct {
		Event           string   `json:"hook_event_name"`
		SessionID       string   `json:"session_id"`
		SessionIDCamel  string   `json:"sessionId"`
		ConversationID  string   `json:"conversation_id"`
		TranscriptPath  string   `json:"transcript_path"`
		TranscriptCamel string   `json:"transcriptPath"`
		Prompt          string   `json:"prompt"`
		CWD             string   `json:"cwd"`
		WorkspaceRoots  []string `json:"workspace_roots"`
	}
	_ = json.Unmarshal(data, &p)

	dir := first(p.CWD, firstOf(p.WorkspaceRoots))
	if dir == "" {
		dir, _ = os.Getwd()
	}
	record.HandleEvent(wasaHome(), record.Event{
		Agent:          *tool,
		AgentSessionID: first(p.SessionID, p.SessionIDCamel, p.ConversationID),
		TranscriptPath: first(p.TranscriptPath, p.TranscriptCamel),
		Prompt:         p.Prompt,
		Dir:            dir,
		WasaSession:    os.Getenv(hook.EnvSession),
		End:            *event == "end" || p.Event == "SessionEnd",
	})
	return nil
}

// first returns its first non-empty argument.
func first(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstOf(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
