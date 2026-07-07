package record

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// state is the live recording context for one session, kept as one JSON file
// under $WASA_HOME/record/ between hook invocations and read once more at
// finish. It is a cache of what the hooks reported: losing it degrades the
// closing checkpoint, never the session.
type state struct {
	SessionID      string    `json:"sessionId"`
	RepoDir        string    `json:"repoDir"`
	WorkspaceID    string    `json:"workspaceId,omitempty"`
	Agent          string    `json:"agent,omitempty"`
	AgentSessionID string    `json:"agentSessionId,omitempty"`
	ResumedFrom    string    `json:"resumedFrom,omitempty"`
	Branch         string    `json:"branch,omitempty"`
	BaseCommit     string    `json:"baseCommit,omitempty"`
	LastHead       string    `json:"lastHead,omitempty"`
	Commits        []string  `json:"commits,omitempty"`
	TranscriptPath string    `json:"transcriptPath,omitempty"`
	Intent         string    `json:"intent,omitempty"`
	Unmanaged      bool      `json:"unmanaged,omitempty"`
	StartedAt      time.Time `json:"startedAt,omitzero"`
}

// meta projects the state into checkpoint metadata.
func (st state) meta() Meta {
	return Meta{
		SessionID:      st.SessionID,
		WorkspaceID:    st.WorkspaceID,
		Agent:          st.Agent,
		AgentSessionID: st.AgentSessionID,
		ResumedFrom:    st.ResumedFrom,
		Branch:         st.Branch,
		BaseCommit:     st.BaseCommit,
		Commits:        slices.Clone(st.Commits),
		StartedAt:      st.StartedAt,
		Unmanaged:      st.Unmanaged,
		WasaVersion:    Version,
	}
}

// statePath is the state file for a session id, which is sanitized because
// unmanaged ids come from the agent's own payload.
func statePath(home, sessionID string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, sessionID)
	return filepath.Join(home, "record", safe+".json")
}

// loadState reads a session's recording state, reporting false when there is
// none or it is unreadable.
func loadState(home, sessionID string) (state, bool) {
	data, err := os.ReadFile(statePath(home, sessionID))
	if err != nil {
		return state{}, false
	}
	var st state
	if json.Unmarshal(data, &st) != nil || st.SessionID == "" {
		return state{}, false
	}
	return st, true
}

// saveState writes a session's recording state via a temp file and rename,
// so a concurrent hook invocation never reads a torn file.
func saveState(home string, st state) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	path := statePath(home, st.SessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".wasa-tmp-*")
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
	return os.Rename(tmpName, path)
}

// removeState drops a session's recording state once its closing checkpoint
// is written.
func removeState(home, sessionID string) {
	_ = os.Remove(statePath(home, sessionID))
}
