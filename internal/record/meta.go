package record

import "time"

// RefPrefix is the namespace every checkpoint ref lives under, one ref per
// checkpoint: refs/wasa/checkpoints/<shard>/<ulid>. It is also the name of
// the pre-ref-store chain ref the writer deletes on sight.
const RefPrefix = "refs/wasa/checkpoints"

// FetchRefspec transfers the whole record to a fresh clone in one fetch:
// git fetch origin "refs/wasa/checkpoints/*:refs/wasa/checkpoints/*".
const FetchRefspec = "refs/wasa/checkpoints/*:refs/wasa/checkpoints/*"

// StorageVersion tags meta.json with the on-disk layout so readers can
// dispatch on it forever after. "refs-1" is the per-checkpoint ref store.
const StorageVersion = "refs-1"

// Version is the wasa build version stamped into every checkpoint's
// meta.json. The CLI entry point sets it at startup; the zero value marks a
// build that never passed through it.
var Version = "dev"

// Meta is the machine-readable half of a checkpoint, stored as meta.json in
// the checkpoint tree.
type Meta struct {
	// SessionID identifies the recorded session; it is also the checkpoint
	// commit's subject line.
	SessionID string `json:"sessionId"`
	// WorkspaceID is wasa's content-addressed repository id. Empty for
	// unmanaged sessions, which have no workspace.
	WorkspaceID string `json:"workspaceId,omitempty"`
	// Agent is the base executable of the recorded agent, e.g. "claude".
	Agent string `json:"agent,omitempty"`
	// AgentSessionID is the agent's own session identifier, captured so a
	// resumed session can hand it to the agent's native resume command.
	AgentSessionID string `json:"agentSessionId,omitempty"`
	// ResumedFrom is the session id this session was resumed from, empty for a
	// session that started fresh.
	ResumedFrom string `json:"resumedFrom,omitempty"`
	// Branch is the branch the session worked on.
	Branch string `json:"branch,omitempty"`
	// BaseCommit is the commit the session's work started from.
	BaseCommit string `json:"baseCommit,omitempty"`
	// Commit is the commit SHA a commit-linked checkpoint points at; empty on
	// the closing checkpoint.
	Commit string `json:"commit,omitempty"`
	// Commits lists every commit SHA the session has produced so far.
	Commits []string `json:"commits,omitempty"`
	// StartedAt is when recording first saw the session.
	StartedAt time.Time `json:"startedAt,omitzero"`
	// FinishedAt is set only on the closing checkpoint.
	FinishedAt time.Time `json:"finishedAt,omitzero"`
	// Unmanaged marks a session recorded via repo-level hooks with no wasa
	// session around it.
	Unmanaged bool `json:"unmanaged,omitempty"`
	// Imported marks a checkpoint backfilled from a pre-existing agent
	// transcript (wasa checkpoints import) rather than recorded live.
	Imported bool `json:"imported,omitempty"`
	// WasaVersion is the wasa build that wrote the checkpoint.
	WasaVersion string `json:"wasaVersion"`
	// StorageVersion is the on-disk layout the checkpoint was written with,
	// so readers can dispatch on it. Always StorageVersion for new writes.
	StorageVersion string `json:"storageVersion"`
	// Gaps notes what a degraded checkpoint is missing (e.g. "transcript
	// unavailable"), so an incomplete record is honest about it.
	Gaps []string `json:"gaps,omitempty"`
}
