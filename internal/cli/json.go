package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/joakimcarlsson/wasa-cli/internal/record"
	"github.com/joakimcarlsson/wasa-cli/internal/registry"
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

// sessionsJSON wraps the session list so the shape can grow without breaking
// consumers that would otherwise depend on a bare top-level array.
type sessionsJSON struct {
	Sessions []*registry.Session `json:"sessions"`
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
	Intent     string    `json:"intent,omitempty"`
	Transcript string    `json:"transcript,omitempty"`
}

// checkpointsJSON wraps the checkpoint list.
type checkpointsJSON struct {
	Checkpoints []checkpointJSON `json:"checkpoints"`
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
