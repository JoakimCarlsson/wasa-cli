package finish

import (
	"errors"
	"slices"
	"testing"

	"github.com/joakimcarlsson/wasa-cli/internal/registry"
)

func TestPauseSequenceKeepsBranch(t *testing.T) {
	ops := &recordingOps{alive: true}

	res, err := Pause(ops, testSession(), false)
	if err != nil {
		t.Fatalf("Pause: %v", err)
	}

	want := []string{
		"TmuxAlive:wasa_ws_sess1",
		"KillTmux:wasa_ws_sess1",
		"RecordCheckpoint:sess1",
		"RemoveWorktree:/wt/feature-x",
	}
	if !slices.Equal(ops.calls, want) {
		t.Fatalf("call sequence = %v, want %v", ops.calls, want)
	}

	if !res.KilledTmux || !res.RemovedWorktree {
		t.Fatalf("result missing a step: %+v", res)
	}
	if res.DeletedBranch {
		t.Fatal("Pause deleted the branch")
	}
	if slices.Contains(ops.calls, "DeleteBranch:feature/x") {
		t.Fatalf("Pause invoked DeleteBranch: %v", ops.calls)
	}
}

func TestPauseDirtyWorktreeBlocks(t *testing.T) {
	ops := &recordingOps{
		alive:     false,
		removeErr: errors.New("worktree is dirty"),
	}

	res, err := Pause(ops, testSession(), false)
	if err == nil {
		t.Fatal("Pause returned nil error for a dirty worktree")
	}
	if res.RemovedWorktree {
		t.Fatal("RemovedWorktree true despite removal failure")
	}
}

func TestPauseForcePropagatesToWorktreeRemoval(t *testing.T) {
	ops := &recordingOps{alive: false}
	if _, err := Pause(ops, testSession(), true); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if !ops.removeForce {
		t.Fatal("force not propagated to RemoveWorktree")
	}
}

func TestPauseNilSession(t *testing.T) {
	if _, err := Pause(&recordingOps{}, nil, false); err == nil {
		t.Fatal("Pause returned nil error for a nil session")
	}
}

// TestPausePlainStopsTmuxOnly asserts that pausing a plain session — no branch,
// no worktree — stops only its tmux: the empty-path guard removes no worktree
// and there is no branch to keep or delete.
func TestPausePlainStopsTmuxOnly(t *testing.T) {
	ops := &recordingOps{alive: true}

	plain := &registry.Session{
		ID:         "plain1",
		WorkingDir: "/some/dir",
		TmuxName:   "wasa__plain1",
	}

	res, err := Pause(ops, plain, false)
	if err != nil {
		t.Fatalf("Pause: %v", err)
	}

	want := []string{
		"TmuxAlive:wasa__plain1",
		"KillTmux:wasa__plain1",
		"RecordCheckpoint:plain1",
	}
	if !slices.Equal(ops.calls, want) {
		t.Fatalf("call sequence = %v, want %v", ops.calls, want)
	}
	if res.RemovedWorktree || res.DeletedBranch {
		t.Fatalf("plain pause touched worktree or branch: %+v", res)
	}
}
