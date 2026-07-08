package record

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/joakimcarlsson/wasa-cli/internal/registry"
	"github.com/joakimcarlsson/wasa-cli/internal/worktree"
)

// commitBurstLimit caps per-commit checkpoints written from one detection.
// An agent commits one at a time, but a pull or rebase can make hundreds of
// commits appear at once; those collapse into a single checkpoint.
const commitBurstLimit = 10

// Event is one agent hook invocation, normalized by the CLI handler.
type Event struct {
	// Agent is the reporting agent's recording tool name, e.g. "claude".
	Agent string
	// AgentSessionID is the agent's own session identifier, used as the
	// recorded session id when no wasa session surrounds it and to locate
	// the transcript when no payload named it.
	AgentSessionID string
	// TranscriptPath is where the agent keeps the session transcript.
	TranscriptPath string
	// Prompt is the user prompt carried by prompt-bearing hook events; the
	// first one seen becomes the session intent.
	Prompt string
	// Dir is the directory the agent runs in.
	Dir string
	// WasaSession is the surrounding wasa session id; empty marks an
	// unmanaged session recorded via repo-level hooks.
	WasaSession string
	// End marks the agent's session-end event, set by the --event end flag
	// the installer wrote on that hook entry.
	End bool
}

// HandleEvent advances a session's recording from one agent hook event: it
// tracks the transcript location and intent, writes a commit-linked
// checkpoint for every commit that landed since the last event, and closes
// an unmanaged session's record on the agent's session-end event (a managed
// session is closed by the finish flow instead). It never returns anything:
// the hook contract is fire-and-forget, so every failure is a silent no-op.
func HandleEvent(home string, ev Event) {
	if _, ok := recorderFor(ev.Agent); !ok || ev.Dir == "" {
		return
	}
	repoDir, err := worktree.Toplevel(ev.Dir)
	if err != nil {
		return
	}
	sid := ev.WasaSession
	if sid == "" {
		sid = ev.AgentSessionID
	}
	if sid == "" {
		return
	}

	st, ok := loadState(home, sid)
	if !ok {
		st = newState(home, sid, repoDir, ev)
	}
	if ev.AgentSessionID != "" {
		st.AgentSessionID = ev.AgentSessionID
	}
	if ev.TranscriptPath != "" {
		st.TranscriptPath = ev.TranscriptPath
	}
	if st.TranscriptPath == "" {
		st.TranscriptPath = fallbackTranscript(
			ev.Agent, ev.AgentSessionID, repoDir,
		)
	}
	if st.Intent == "" {
		st.Intent = strings.TrimSpace(ev.Prompt)
	}
	if st.Intent == "" {
		transcript, _ := os.ReadFile(st.TranscriptPath)
		st.Intent = intentFrom(st.Agent, transcript)
	}

	if head := headSHA(repoDir); head != "" && head != st.LastHead {
		st = checkpointNewCommits(repoDir, st, head)
	}

	_ = saveState(home, st)
	if ev.End && st.Unmanaged {
		startFinalize(home, sid)
	}
}

// startFinalize writes an unmanaged session's closing checkpoint. In
// production it spawns a detached child (spawnFinalize) so that an agent
// cancelling its SessionEnd hook — as headless Claude Code does on exit —
// cannot abort the write; tests override it to run Finalize inline.
var startFinalize = spawnFinalize

// spawnFinalize launches "wasa record-finalize --session <sid>" in its own
// session, so the closing checkpoint is written off the hook's lifecycle and
// survives the hook process being killed. The child re-derives $WASA_HOME from
// the inherited environment. Best-effort: an exec failure leaves the state on
// disk for the next run to finalize.
func spawnFinalize(_, sid string) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "record-finalize", "--session", sid)
	cmd.Env = os.Environ()
	detach(cmd)
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
}

// Finalize writes a session's closing checkpoint from its persisted recording
// state: any commits since the last event, the final transcript, the commit
// list and the finish timestamp, then drops the state. It is what the detached
// finalizer runs for an unmanaged session. A session with no state or no known
// repository is a no-op.
func Finalize(home, sid string) error {
	st, ok := loadState(home, sid)
	if !ok || st.RepoDir == "" {
		return nil
	}
	if head := headSHA(st.RepoDir); head != "" && head != st.LastHead {
		st = checkpointNewCommits(st.RepoDir, st, head)
	}
	native, _ := os.ReadFile(st.TranscriptPath)
	m := st.meta()
	m.FinishedAt = time.Now()
	if len(native) == 0 {
		m.Gaps = append(m.Gaps, "transcript unavailable")
	}
	ref, err := Write(st.RepoDir, Checkpoint{
		Meta: m, Intent: st.Intent, Transcript: normalize(st.Agent, native),
	})
	if err != nil {
		return err
	}
	removeState(home, sid)
	pushDetached(st.RepoDir, []string{ref})
	return nil
}

// newState seeds the recording state the first time a session is seen. A
// managed session is enriched from the registry, which knows its workspace,
// branch and base commit authoritatively; an unmanaged session gets what git
// can tell about the directory it runs in.
func newState(home, sid, repoDir string, ev Event) state {
	st := state{
		SessionID:  sid,
		RepoDir:    repoDir,
		Agent:      ev.Agent,
		Branch:     headBranch(repoDir),
		BaseCommit: headSHA(repoDir),
		Unmanaged:  ev.WasaSession == "",
		StartedAt:  time.Now(),
	}
	st.LastHead = st.BaseCommit
	if st.Unmanaged {
		return st
	}
	reg, err := registry.Open(home)
	if err != nil {
		return st
	}
	s, ok := reg.Session(sid)
	if !ok {
		return st
	}
	st.WorkspaceID = s.WorkspaceID
	st.ResumedFrom = s.ResumedFrom
	if s.Branch != "" {
		st.Branch = s.Branch
	}
	if s.BaseCommit != "" {
		st.BaseCommit = s.BaseCommit
	}
	if !s.CreatedAt.IsZero() {
		st.StartedAt = s.CreatedAt
	}
	return st
}

// checkpointNewCommits writes one commit-linked checkpoint per commit that
// landed since the state's last look at HEAD, collapsing a burst larger than
// commitBurstLimit into a single checkpoint so a pull or rebase cannot flood
// the ref. Checkpoint failures are skipped silently per the degradation
// contract; the head is still advanced so the same commits are not retried
// on every subsequent event.
func checkpointNewCommits(repoDir string, st state, head string) state {
	newCommits := commitsBetween(repoDir, st.LastHead, head)
	if len(newCommits) == 0 {
		st.LastHead = head
		return st
	}
	native, _ := os.ReadFile(st.TranscriptPath)
	transcript := normalize(st.Agent, native)

	var burst []string
	if len(newCommits) > commitBurstLimit {
		burst = newCommits[:len(newCommits)-1]
		newCommits = newCommits[len(newCommits)-1:]
	}
	st.Commits = append(st.Commits, burst...)
	var refs []string
	for _, c := range newCommits {
		st.Commits = append(st.Commits, c)
		m := st.meta()
		m.Commit = c
		if len(burst) > 0 {
			m.Gaps = append(m.Gaps, fmt.Sprintf(
				"%d commits arrived at once; only the newest is linked",
				len(burst)+1,
			))
		}
		if len(native) == 0 {
			m.Gaps = append(m.Gaps, "transcript unavailable")
		}
		if ref, err := Write(repoDir, Checkpoint{
			Meta: m, Intent: st.Intent, Transcript: transcript,
		}); err == nil {
			refs = append(refs, ref)
		}
	}
	st.LastHead = head
	pushDetached(repoDir, refs)
	return st
}

// FinishInfo describes a finishing session as the teardown flow knows it.
// Every field is optional except SessionID; whatever the finish flow does
// not know is filled from the recorded state, and what neither knows is
// noted as a gap.
type FinishInfo struct {
	SessionID   string
	WorkspaceID string
	// Program is the launch program; its base executable is recorded as the
	// agent.
	Program    string
	Branch     string
	BaseCommit string
	// RepoDir is the repository the session ran against.
	RepoDir   string
	StartedAt time.Time
	// ResumedFrom is the session id this session was resumed from, empty when
	// the session started fresh.
	ResumedFrom string
}

// Finish writes a session's closing checkpoint: the final transcript, the
// full commit list and the finish timestamp. A session that produced zero
// commits is still recorded — failed attempts are history worth keeping. It
// returns at most one error for the caller to log; teardown must never act
// on it. A session with no known repository (a plain session outside any
// repo, with no hook data) records nothing and returns nil.
func Finish(home string, info FinishInfo) error {
	if info.SessionID == "" {
		return nil
	}
	st, hasState := loadState(home, info.SessionID)

	repoDir := info.RepoDir
	if repoDir == "" {
		repoDir = st.RepoDir
	}
	if repoDir == "" {
		return nil
	}

	m := st.meta()
	m.SessionID = info.SessionID
	m.Unmanaged = false
	if info.WorkspaceID != "" {
		m.WorkspaceID = info.WorkspaceID
	}
	if agent := baseExe(info.Program); agent != "" {
		m.Agent = agent
	}
	if info.Branch != "" {
		m.Branch = info.Branch
	}
	if info.BaseCommit != "" {
		m.BaseCommit = info.BaseCommit
	}
	if info.ResumedFrom != "" {
		m.ResumedFrom = info.ResumedFrom
	}
	if !info.StartedAt.IsZero() {
		m.StartedAt = info.StartedAt
	}
	m.FinishedAt = time.Now()
	if !hasState {
		m.Gaps = append(m.Gaps, "no hook data received")
	}

	if m.Branch != "" && m.BaseCommit != "" {
		if tip, err := gitIn(
			repoDir, nil, "rev-parse", "--verify", "-q",
			"refs/heads/"+m.Branch,
		); err == nil {
			m.Commits = commitsBetween(repoDir, m.BaseCommit, tip)
		}
	}

	var native []byte
	if st.TranscriptPath != "" {
		native, _ = os.ReadFile(st.TranscriptPath)
	}
	if len(native) == 0 {
		m.Gaps = append(m.Gaps, "transcript unavailable")
	}
	intent := st.Intent
	if intent == "" {
		intent = intentFrom(m.Agent, native)
	}

	ref, err := Write(repoDir, Checkpoint{
		Meta: m, Intent: intent, Transcript: normalize(m.Agent, native),
	})
	if err != nil {
		return err
	}
	removeState(home, info.SessionID)
	if err := Push(repoDir, ref); err != nil {
		log.Printf("wasa: checkpoint sync skipped: %v", err)
	}
	return nil
}

// FinishSession is the shared finish.Ops RecordCheckpoint implementation:
// it writes s's closing checkpoint against repoDir (empty for a plain
// session outside any workspace, in which case the hook-reported state fills
// in what it can) and degrades any failure to a single logged warning, so
// recording can never fail a teardown.
func FinishSession(home, repoDir string, s *registry.Session) {
	if s == nil {
		return
	}
	err := Finish(home, FinishInfo{
		SessionID:   s.ID,
		WorkspaceID: s.WorkspaceID,
		Program:     s.Program,
		Branch:      s.Branch,
		BaseCommit:  s.BaseCommit,
		RepoDir:     repoDir,
		StartedAt:   s.CreatedAt,
		ResumedFrom: s.ResumedFrom,
	})
	if err != nil {
		log.Printf("wasa: session %s not recorded: %v", s.ID, err)
	}
}

// baseExe reduces a launch program ("/usr/bin/claude --resume") to its base
// executable name ("claude"), the value recorded as the agent.
func baseExe(program string) string {
	program = strings.TrimSpace(program)
	if program == "" {
		return ""
	}
	return filepath.Base(strings.Fields(program)[0])
}
