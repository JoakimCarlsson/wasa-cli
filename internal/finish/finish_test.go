package finish

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joakimcarlsson/wasa/internal/registry"
)

// recordingOps records every operation invoked on it, in order, so a test can
// assert the exact teardown sequence. Its method set is the whole vocabulary
// finish has access to; there is no merge, rebase, push or pull-request method
// to call, so the absence of one is structural, and the recorded log lets a test
// assert it explicitly besides.
type recordingOps struct {
	alive       bool
	aliveErr    error
	killErr     error
	removeErr   error
	branchErr   error
	calls       []string
	removeForce bool
}

func (o *recordingOps) TmuxAlive(name string) (bool, error) {
	o.calls = append(o.calls, "TmuxAlive:"+name)
	return o.alive, o.aliveErr
}

func (o *recordingOps) KillTmux(name string) error {
	o.calls = append(o.calls, "KillTmux:"+name)
	return o.killErr
}

func (o *recordingOps) RemoveWorktree(path string, force bool) error {
	o.removeForce = force
	o.calls = append(o.calls, "RemoveWorktree:"+path)
	return o.removeErr
}

func (o *recordingOps) DeleteBranch(branch string) error {
	o.calls = append(o.calls, "DeleteBranch:"+branch)
	return o.branchErr
}

func testSession() *registry.Session {
	return &registry.Session{
		ID:           "sess1",
		Branch:       "feature/x",
		WorktreePath: "/wt/feature-x",
		TmuxName:     "wasa_ws_sess1",
	}
}

func TestSessionTeardownSequence(t *testing.T) {
	ops := &recordingOps{alive: true}

	res, err := Session(ops, testSession(), false)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}

	want := []string{
		"TmuxAlive:wasa_ws_sess1",
		"KillTmux:wasa_ws_sess1",
		"RemoveWorktree:/wt/feature-x",
		"DeleteBranch:feature/x",
	}
	if !slices.Equal(ops.calls, want) {
		t.Fatalf("call sequence = %v, want %v", ops.calls, want)
	}

	if !res.KilledTmux || !res.RemovedWorktree || !res.DeletedBranch {
		t.Fatalf("result missing a step: %+v", res)
	}
	if res.Branch != "feature/x" || res.WorktreePath != "/wt/feature-x" {
		t.Fatalf("result identifiers = %+v", res)
	}
}

func TestSessionNeverMerges(t *testing.T) {
	ops := &recordingOps{alive: true}
	if _, err := Session(ops, testSession(), false); err != nil {
		t.Fatalf("Session: %v", err)
	}

	for _, call := range ops.calls {
		lower := strings.ToLower(call)
		for _, forbidden := range []string{"merge", "rebase", "push", "pull", "pr"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("teardown invoked a forbidden operation: %q", call)
			}
		}
	}

	if hasMergeLikeMethod(ops) {
		t.Fatal("Ops exposes a merge-like method; finish could merge")
	}
}

// hasMergeLikeMethod reports whether the Ops interface offers any method whose
// name suggests merging, rebasing, pushing or opening a pull request. It guards
// the hard no-merge rule at the type level: if such a method is ever added to
// Ops, this test fails even before any call sequence is checked.
func hasMergeLikeMethod(ops Ops) bool {
	for m := range reflect.TypeOf(ops).Methods() {
		name := strings.ToLower(m.Name)
		for _, forbidden := range []string{"merge", "rebase", "push", "pull", "pr"} {
			if strings.Contains(name, forbidden) {
				return true
			}
		}
	}
	return false
}

func TestSessionSkipsKillWhenTmuxDead(t *testing.T) {
	ops := &recordingOps{alive: false}

	res, err := Session(ops, testSession(), false)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if res.KilledTmux {
		t.Fatal("KilledTmux true for an already-dead tmux session")
	}
	if slices.Contains(ops.calls, "KillTmux:wasa_ws_sess1") {
		t.Fatalf("KillTmux called for a dead session: %v", ops.calls)
	}
	if !res.RemovedWorktree || !res.DeletedBranch {
		t.Fatalf("teardown did not continue past tmux: %+v", res)
	}
}

func TestSessionDirtyWorktreeBlocksBranchDelete(t *testing.T) {
	ops := &recordingOps{
		alive:     false,
		removeErr: errors.New("worktree is dirty"),
	}

	res, err := Session(ops, testSession(), false)
	if err == nil {
		t.Fatal("Session returned nil error for a dirty worktree")
	}
	if res.RemovedWorktree {
		t.Fatal("RemovedWorktree true despite removal failure")
	}
	if slices.Contains(ops.calls, "DeleteBranch:feature/x") {
		t.Fatalf(
			"branch deleted despite worktree-removal failure: %v",
			ops.calls,
		)
	}
	if res.DeletedBranch {
		t.Fatal("DeletedBranch true despite worktree-removal failure")
	}
}

func TestSessionForcePropagatesToWorktreeRemoval(t *testing.T) {
	ops := &recordingOps{alive: false}
	if _, err := Session(ops, testSession(), true); err != nil {
		t.Fatalf("Session: %v", err)
	}
	if !ops.removeForce {
		t.Fatal("force not propagated to RemoveWorktree")
	}
}

func TestSessionNilSession(t *testing.T) {
	if _, err := Session(&recordingOps{}, nil, false); err == nil {
		t.Fatal("Session returned nil error for a nil session")
	}
}

// TestSessionPlainTearsDownTmuxOnly asserts that tearing down a plain session —
// one with no branch and no worktree — stops only its tmux: it removes no
// worktree and deletes no branch, because the empty-path and empty-branch guards
// skip both. A plain session launched outside any repository (empty workspace)
// behaves identically.
func TestSessionPlainTearsDownTmuxOnly(t *testing.T) {
	ops := &recordingOps{alive: true}

	plain := &registry.Session{
		ID:         "plain1",
		WorkingDir: "/some/dir",
		TmuxName:   "wasa__plain1",
	}

	res, err := Session(ops, plain, false)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}

	want := []string{"TmuxAlive:wasa__plain1", "KillTmux:wasa__plain1"}
	if !slices.Equal(ops.calls, want) {
		t.Fatalf(
			"call sequence = %v, want only the tmux teardown %v",
			ops.calls,
			want,
		)
	}
	if !res.KilledTmux {
		t.Fatal("KilledTmux false for a live plain session")
	}
	if res.RemovedWorktree {
		t.Fatal("RemovedWorktree true for a worktree-less plain session")
	}
	if res.DeletedBranch {
		t.Fatal("DeletedBranch true for a branchless plain session")
	}
}
