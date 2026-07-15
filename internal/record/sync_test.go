package record

import (
	"os/exec"
	"sort"
	"strings"
	"testing"
)

// initBareRemote creates a bare repository to stand in for origin.
func initBareRemote(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := gitIn(dir, nil, "init", "-q", "--bare"); err != nil {
		t.Fatalf("init bare: %v", err)
	}
	return dir
}

func TestPushAllAndPullAllConvergeAcrossTwoRunners(t *testing.T) {
	origin := initBareRemote(t)

	runnerA := initRepo(t)
	mustGit(t, runnerA, "remote", "add", "origin", origin)
	refA := mustWrite(t, runnerA, Checkpoint{
		Meta: Meta{SessionID: "runnerA-session", WasaVersion: "test"},
	})

	runnerB := initRepo(t)
	mustGit(t, runnerB, "remote", "add", "origin", origin)
	refB := mustWrite(t, runnerB, Checkpoint{
		Meta: Meta{SessionID: "runnerB-session", WasaVersion: "test"},
	})

	// Each runner pushes its own, disjoint ref: neither should be rejected
	// by the other's push, because per-ref uniqueness (Write's ULID scheme)
	// makes this a set union rather than a mutable-ref race.
	resA, err := PushAll(runnerA, "origin")
	if err != nil {
		t.Fatalf("PushAll(runnerA): %v", err)
	}
	if len(resA.Refs) != 1 || resA.Refs[0] != refA {
		t.Fatalf("PushAll(runnerA).Refs = %v, want [%s]", resA.Refs, refA)
	}

	resB, err := PushAll(runnerB, "origin")
	if err != nil {
		t.Fatalf("PushAll(runnerB): %v", err)
	}
	if len(resB.Refs) != 1 || resB.Refs[0] != refB {
		t.Fatalf("PushAll(runnerB).Refs = %v, want [%s]", resB.Refs, refB)
	}

	// runnerA pulls: it should gain runnerB's ref without losing its own.
	pullRes, err := PullAll(runnerA, "origin")
	if err != nil {
		t.Fatalf("PullAll(runnerA): %v", err)
	}
	if len(pullRes.Refs) != 1 || pullRes.Refs[0] != refB {
		t.Fatalf("PullAll(runnerA).Refs = %v, want [%s]", pullRes.Refs, refB)
	}

	entries, err := List(runnerA)
	if err != nil {
		t.Fatalf("List(runnerA): %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf(
			"List(runnerA) after union = %d entries, want 2 (both runners' records)",
			len(entries),
		)
	}
	var sessions []string
	for _, e := range entries {
		sessions = append(sessions, e.Meta.SessionID)
	}
	sort.Strings(sessions)
	want := []string{"runnerA-session", "runnerB-session"}
	if strings.Join(sessions, ",") != strings.Join(want, ",") {
		t.Fatalf("sessions = %v, want %v", sessions, want)
	}

	// runnerA's own ref must survive the pull unchanged.
	if _, err := gitIn(
		runnerA, nil, "rev-parse", "--verify", "-q", refA,
	); err != nil {
		t.Fatalf("runnerA lost its own ref %s after pull: %v", refA, err)
	}
}

func TestPullAllBootstrapsFreshClone(t *testing.T) {
	origin := initBareRemote(t)

	writer := initRepo(t)
	mustGit(t, writer, "remote", "add", "origin", origin)
	ref := mustWrite(t, writer, Checkpoint{
		Meta: Meta{SessionID: "s1", WasaVersion: "test"},
	})
	if _, err := PushAll(writer, "origin"); err != nil {
		t.Fatalf("PushAll: %v", err)
	}

	fresh := t.TempDir()
	if out, err := exec.Command(
		"git", "clone", "-q", origin, fresh,
	).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v: %s", err, out)
	}

	res, err := PullAll(fresh, "origin")
	if err != nil {
		t.Fatalf("PullAll(fresh clone): %v", err)
	}
	if len(res.Refs) != 1 || res.Refs[0] != ref {
		t.Fatalf("PullAll(fresh).Refs = %v, want [%s]", res.Refs, ref)
	}

	entries, err := List(fresh)
	if err != nil || len(entries) != 1 {
		t.Fatalf("List(fresh) = %v, %v; want 1 entry", entries, err)
	}
}

func TestPushAllSecondRunIsUpToDate(t *testing.T) {
	origin := initBareRemote(t)
	dir := initRepo(t)
	mustGit(t, dir, "remote", "add", "origin", origin)
	mustWrite(t, dir, Checkpoint{
		Meta: Meta{SessionID: "s1", WasaVersion: "test"},
	})

	if _, err := PushAll(dir, "origin"); err != nil {
		t.Fatalf("first PushAll: %v", err)
	}
	res, err := PushAll(dir, "origin")
	if err != nil {
		t.Fatalf("second PushAll: %v", err)
	}
	if len(res.Refs) != 0 {
		t.Fatalf("second PushAll.Refs = %v, want none (already up to date)",
			res.Refs)
	}
}

func TestPushAllAndPullAllWithoutRemoteFail(t *testing.T) {
	dir := initRepo(t)
	mustWrite(t, dir, Checkpoint{
		Meta: Meta{SessionID: "s1", WasaVersion: "test"},
	})
	if _, err := PushAll(dir, "origin"); err == nil {
		t.Error("PushAll without a remote should error")
	}
	if _, err := PullAll(dir, "origin"); err == nil {
		t.Error("PullAll without a remote should error")
	}
}

func TestParsePushPorcelain(t *testing.T) {
	out := "To origin\n" +
		"*\trefs/wasa/checkpoints/a/1:refs/wasa/checkpoints/a/1\t[new reference]\n" +
		"=\trefs/wasa/checkpoints/a/2:refs/wasa/checkpoints/a/2\t[up to date]\n" +
		"!\trefs/wasa/checkpoints/a/3:refs/wasa/checkpoints/a/3\t[rejected]\n" +
		"Done\n"
	changed, rejected := parsePushPorcelain(out)
	if len(changed) != 1 || changed[0] != "refs/wasa/checkpoints/a/1" {
		t.Errorf("changed = %v", changed)
	}
	if len(rejected) != 1 || rejected[0] != "refs/wasa/checkpoints/a/3" {
		t.Errorf("rejected = %v", rejected)
	}
}
