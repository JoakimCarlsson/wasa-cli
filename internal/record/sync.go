package record

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// syncTimeout bounds an explicit, user-invoked push or pull. It is longer
// than pushTimeout because the caller is waiting on a terminal and expects a
// real answer (success or a reported failure), not a background fire-and
// forget.
const syncTimeout = 60 * time.Second

// SyncResult reports the refs a push or pull actually transferred, so the
// caller can print a summary instead of a bare "ok".
type SyncResult struct {
	// Refs lists the refs/wasa/* refs that changed: new on the destination
	// or moved to a new object. Unchanged refs (already in sync on both
	// ends) are omitted.
	Refs []string
}

// PullAll fetches the whole refs/wasa/* namespace from remote and integrates
// it into the local namespace: every namespace under refs/wasa (today
// checkpoints, tomorrow reviews) transfers in one command, with no code
// change required when a new namespace is added. A fetch is additive — it
// only ever creates or fast-forwards local refs to match the remote, so a
// local-only ref another runner has not pushed yet survives untouched. On a
// fresh clone this populates the entire record in one call. Auth failures, a
// missing remote and rejected updates come back as a real error instead of
// being swallowed.
func PullAll(repoDir, remote string) (SyncResult, error) {
	before, err := wasaRefs(repoDir)
	if err != nil {
		return SyncResult{}, fmt.Errorf("pull: %w", err)
	}
	if _, err := runSyncGit(
		repoDir, "fetch", remote, SyncRefspec,
	); err != nil {
		return SyncResult{}, fmt.Errorf("pull: %w", err)
	}
	after, err := wasaRefs(repoDir)
	if err != nil {
		return SyncResult{}, fmt.Errorf("pull: %w", err)
	}
	return SyncResult{Refs: changedRefs(before, after)}, nil
}

// PushAll pushes the whole local refs/wasa/* namespace to remote. Because
// every checkpoint and review lives on its own uniquely named ref (see
// Write's ULID-per-ref scheme), this is a set union, not an overwrite: two
// runners pushing concurrently against the same origin each add their own
// refs and neither clobbers the other's. It is not a mirror push — refs the
// remote has that the local clone lacks are left alone, and a rejected
// update (e.g. a non-fast-forward on a ref that should never move) is
// reported rather than forced through.
func PushAll(repoDir, remote string) (SyncResult, error) {
	out, err := runSyncGit(
		repoDir, "push", "--porcelain", remote, SyncRefspec,
	)
	if err != nil {
		return SyncResult{}, fmt.Errorf("push: %w", err)
	}
	refs, rejected := parsePushPorcelain(out)
	if len(rejected) > 0 {
		return SyncResult{Refs: refs}, fmt.Errorf(
			"push: rejected %d ref(s): %s",
			len(rejected), strings.Join(rejected, ", "),
		)
	}
	return SyncResult{Refs: refs}, nil
}

// runSyncGit runs a push or pull's git subcommand with credential prompts
// disabled (the same non-interactive env the best-effort hook push uses, so
// an explicit sync fails fast instead of hanging on a prompt) and a longer
// timeout than the background push, since the caller is waiting on it.
func runSyncGit(repoDir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), syncTimeout)
	defer cancel()
	cmd := exec.CommandContext(
		ctx, "git", append([]string{"-C", repoDir}, args...)...,
	)
	cmd.Env = pushEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf(
			"git %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)),
		)
	}
	return string(out), nil
}

// wasaRefs returns every ref under refs/wasa/ and the object it points at,
// so a pull can diff before and after a fetch to report what arrived.
func wasaRefs(repoDir string) (map[string]string, error) {
	out, err := gitIn(
		repoDir, nil,
		"for-each-ref", "--format=%(refname) %(objectname)", "refs/wasa/",
	)
	if err != nil {
		return nil, fmt.Errorf("list refs: %w", err)
	}
	refs := make(map[string]string)
	if out == "" {
		return refs, nil
	}
	for _, line := range strings.Split(out, "\n") {
		name, obj, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		refs[name] = obj
	}
	return refs, nil
}

// changedRefs returns the refs present in after that are new or point at a
// different object than in before, sorted for stable output.
func changedRefs(before, after map[string]string) []string {
	var changed []string
	for name, obj := range after {
		if before[name] != obj {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	return changed
}

// parsePushPorcelain parses `git push --porcelain` output into the refs that
// changed and the refs a rejected/errored status line named. See
// git-push(1): each ref-update line is "<flag>\t<from>:<to>\t<summary>";
// "=" means the ref was already up to date, "!" is a rejection or error.
func parsePushPorcelain(out string) (changed, rejected []string) {
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "\t") {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 2 {
			continue
		}
		flag, spec := fields[0], fields[1]
		_, dst, ok := strings.Cut(spec, ":")
		if !ok {
			dst = spec
		}
		switch flag {
		case "=":
			// Already up to date: nothing changed.
		case "!":
			rejected = append(rejected, dst)
		default:
			changed = append(changed, dst)
		}
	}
	sort.Strings(changed)
	sort.Strings(rejected)
	return changed, rejected
}
