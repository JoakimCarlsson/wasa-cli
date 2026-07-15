package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/joakimcarlsson/wasa-cli/internal/record"
	"github.com/joakimcarlsson/wasa-cli/internal/registry"
	"github.com/joakimcarlsson/wasa-cli/internal/sessionstatus"
)

// outputContract is the version of the --json output contract. Consumers read
// it from `wasa version --json` and refuse to parse output from a wasa whose
// contract they do not understand. Bump it only on a backward-incompatible
// change to any --json payload; additive fields do not bump it.
const outputContract = 1

// jsonFlag registers a uniform --json flag on fs and returns its target, so
// every read command selects machine-readable output the same way.
func jsonFlag(fs *flag.FlagSet) *bool {
	return fs.Bool("json", false, "emit machine-readable JSON to stdout")
}

// emitJSON writes v to w as indented JSON followed by a newline. It marshals
// fully before writing, so a marshal error leaves w untouched and no partial
// document reaches stdout.
func emitJSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}

// versionJSON is the payload of `wasa version --json`: the build version and
// the structured-output contract version a consumer must gate on.
type versionJSON struct {
	Version  string `json:"version"`
	Contract int    `json:"contract"`
}

// sessionJSON is one session in list output: the registry record verbatim, plus
// a derived activityStatus the registry never persists. Embedding the record
// keeps every stored field at the top level with its own tag, so adding the
// derived field stays backward compatible.
type sessionJSON struct {
	*registry.Session
	ActivityStatus string `json:"activityStatus"`
}

// sessionsJSON wraps the session list so the shape can grow without breaking
// consumers that would otherwise depend on a bare top-level array.
type sessionsJSON struct {
	Sessions []sessionJSON `json:"sessions"`
}

// sessionCreatedJSON is the payload of `wasa session new --json`: the created
// session in the same shape as a list item, wrapped in an object so the shape
// can grow without breaking consumers.
type sessionCreatedJSON struct {
	Session sessionJSON `json:"session"`
}

// sessionCreatedPayload builds the --json output for a freshly created session,
// deriving its activityStatus as of now exactly as the list command does.
func sessionCreatedPayload(
	home string,
	s *registry.Session,
	now time.Time,
) sessionCreatedJSON {
	return sessionCreatedJSON{
		Session: sessionJSON{
			Session:        s,
			ActivityStatus: activityFor(home, s, now),
		},
	}
}

// activityFor decodes a session into the single status a consumer renders,
// folding lifecycle and activity into one value: a paused session is "paused"; an
// exited one is "finished" or "failed" by its captured exit code, or plain
// "exited" when no code was recorded (killed or signalled); a running one's
// working/waiting/idle activity comes from its hook record via
// sessionstatus.Derive, defaulting to "running".
//
// The list command has no live pane-capture history — that is the TUI's, built
// from successive captures over time — so it passes Unknown as the scraped status
// and relies on hook records alone. An agent with no hook channel therefore reads
// "running" here even while it waits for input.
func activityFor(home string, s *registry.Session, now time.Time) string {
	switch s.Status {
	case registry.StatusPaused:
		return "paused"
	case registry.StatusExited:
		switch {
		case s.ExitCode != nil && *s.ExitCode == 0:
			return "finished"
		case s.ExitCode != nil:
			return "failed"
		default:
			return "exited"
		}
	default:
		return sessionstatus.Derive(home, s.ID, sessionstatus.Unknown, now).
			Label()
	}
}

// sessionsPayload builds the --json session list, deriving each session's
// activityStatus as of now.
func sessionsPayload(
	home string,
	sessions []*registry.Session,
	now time.Time,
) sessionsJSON {
	out := make([]sessionJSON, len(sessions))
	for i, s := range sessions {
		out[i] = sessionJSON{
			Session:        s,
			ActivityStatus: activityFor(home, s, now),
		}
	}
	return sessionsJSON{Sessions: out}
}

// workspacesJSON wraps the workspace list, for the same reason as sessionsJSON.
type workspacesJSON struct {
	Workspaces []*registry.Workspace `json:"workspaces"`
}

// checkpointJSON is one recorded session in --json output. It embeds the
// checkpoint meta so its fields (sessionId, branch, …) sit at the top level,
// and adds the derived state plus, for a single-checkpoint show, the intent and
// rendered transcript.
type checkpointJSON struct {
	record.Meta
	CommitSHA  string    `json:"commitSHA,omitempty"`
	When       time.Time `json:"when"`
	State      string    `json:"state"`
	Signature  string    `json:"signature,omitempty"`
	Intent     string    `json:"intent,omitempty"`
	Transcript string    `json:"transcript,omitempty"`
}

// checkpointsJSON wraps the checkpoint list.
type checkpointsJSON struct {
	Checkpoints []checkpointJSON `json:"checkpoints"`
}

// explainPayload builds the --json output of `checkpoints explain`: one
// checkpointJSON per matching checkpoint, each with its intent and — unless
// withoutTranscript — its rendered transcript, in the same wrapper the list
// uses. It mirrors the show command's single-checkpoint payload for every
// match, so a consumer reads explain and show output the same way.
func explainPayload(
	matches []record.Match,
	withoutTranscript bool,
) checkpointsJSON {
	items := make([]checkpointJSON, 0, len(matches))
	for _, m := range matches {
		item := checkpointJSON{
			Meta:      m.Meta,
			CommitSHA: m.CommitSHA,
			When:      m.When,
			State:     checkpointState(m.Meta),
			Signature: string(m.Signature),
			Intent:    m.Intent,
		}
		if !withoutTranscript && len(m.Transcript) != 0 {
			item.Transcript = record.RenderTranscript(m.Transcript)
		}
		items = append(items, item)
	}
	return checkpointsJSON{Checkpoints: items}
}

// checkpointState derives the human "state" column value from a checkpoint's
// meta: open vs finished, annotated as imported or unmanaged. It mirrors the
// tabwriter list so both surfaces agree.
func checkpointState(m record.Meta) string {
	state := "open"
	if !m.FinishedAt.IsZero() {
		state = "finished"
	}
	if m.Imported {
		state += ", imported"
	} else if m.Unmanaged {
		state += ", unmanaged"
	}
	return state
}
