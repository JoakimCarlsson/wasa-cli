package record

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// pushTimeout bounds the best-effort sync so a slow network can never stall
// a hook invocation or a session finish.
const pushTimeout = 15 * time.Second

// Checkpoint is one record written to the ref: the metadata, the prompt that
// started the session and the agent conversation so far.
type Checkpoint struct {
	Meta       Meta
	Intent     string
	Transcript []byte
}

// Write commits cp to its own ref refs/wasa/checkpoints/<shard>/<ulid> in the
// repository containing repoDir (a main checkout or a linked worktree; the
// ref and objects live in the shared git dir either way) and returns the ref
// it wrote. The transcript and the intent — which is usually lifted straight
// from the transcript — are redacted before they enter the object database.
// Only plumbing runs — hash-object, mktree, commit-tree, update-ref — so
// branches, index, working copy and reflog are untouched. The commit is an
// orphan (no parent): per-checkpoint history is the ref's own reflog problem,
// not a chain. Each ref is unique, so there is no ref race to retry.
func Write(repoDir string, cp Checkpoint) (string, error) {
	if cp.Meta.SessionID == "" {
		return "", fmt.Errorf("checkpoint has no session id")
	}
	dropLegacyRef(repoDir)
	cp.Meta.StorageVersion = StorageVersion
	metaJSON, err := json.MarshalIndent(cp.Meta, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal meta: %w", err)
	}

	files := []struct {
		name string
		data []byte
	}{
		{"intent.md", Redact([]byte(cp.Intent))},
		{"meta.json", append(metaJSON, '\n')},
		{"transcript.jsonl", Redact(cp.Transcript)},
	}
	var tree strings.Builder
	for _, f := range files {
		sha, err := gitIn(
			repoDir, bytes.NewReader(f.data), "hash-object", "-w", "--stdin",
		)
		if err != nil {
			return "", fmt.Errorf("hash %s: %w", f.name, err)
		}
		fmt.Fprintf(&tree, "100644 blob %s\t%s\n", sha, f.name)
	}
	treeSHA, err := gitIn(repoDir, strings.NewReader(tree.String()), "mktree")
	if err != nil {
		return "", fmt.Errorf("mktree: %w", err)
	}

	commit, err := gitIn(
		repoDir, nil, "commit-tree", treeSHA, "-m", cp.Meta.SessionID,
	)
	if err != nil {
		return "", fmt.Errorf("commit-tree: %w", err)
	}
	id := newULID()
	ref := RefPrefix + "/" + shard(id) + "/" + id
	if _, err := gitIn(repoDir, nil, "update-ref", ref, commit); err != nil {
		return "", fmt.Errorf("update %s: %w", ref, err)
	}
	return ref, nil
}

// dropLegacyRef deletes the pre-ref-store chain ref if it is present. The old
// single ref refs/wasa/checkpoints and the new refs/wasa/checkpoints/<shard>/
// namespace are a git directory/file conflict — git will not create the
// sharded refs while the chain ref exists — so the writer clears it on sight.
// A missing ref is the normal case and its error is ignored.
func dropLegacyRef(repoDir string) {
	_, _ = gitIn(repoDir, nil, "update-ref", "-d", RefPrefix)
}

// Push best-effort syncs the named checkpoint refs to origin in one push.
// Offline, no origin or no permission are all expected outcomes; the caller
// decides whether the returned error is worth one log line. The push is
// non-atomic, so one ref being rejected does not stop the others. Credential
// prompts are disabled — terminal and GUI alike — so an unauthenticated push
// fails fast instead of hanging a hook invocation on a prompt nobody can see.
func Push(repoDir string, refs ...string) error {
	if len(refs) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
	defer cancel()
	cmd := exec.CommandContext(
		ctx, "git", append([]string{"-C", repoDir, "push", "origin"},
			refspecs(refs)...)...,
	)
	cmd.Env = pushEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"push checkpoints: %w: %s", err, strings.TrimSpace(string(out)),
		)
	}
	return nil
}

// pushDetached fire-and-forgets the sync from hook context: the hook process
// must exit immediately (an agent cancels slow hooks and surfaces the noise),
// so the push runs in its own session and outlives it. No timeout: prompts
// are disabled, so git either finishes or fails on its own.
func pushDetached(repoDir string, refs []string) {
	if len(refs) == 0 {
		return
	}
	cmd := exec.Command(
		"git", append([]string{"-C", repoDir, "push", "origin"},
			refspecs(refs)...)...,
	)
	cmd.Env = pushEnv()
	detach(cmd)
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
}

// refspecs turns checkpoint refs into <ref>:<ref> push refspecs.
func refspecs(refs []string) []string {
	specs := make([]string, len(refs))
	for i, r := range refs {
		specs[i] = r + ":" + r
	}
	return specs
}

// pushEnv disables credential prompts, terminal and GUI alike, so an
// unauthenticated push fails fast instead of hanging on a prompt nobody can
// see.
func pushEnv() []string {
	return append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=echo",
		"GCM_INTERACTIVE=never",
	)
}

// gitIn runs git -C dir with an optional stdin and returns trimmed stdout.
// Checkpoint commits get a fixed machine identity so recording works in
// repositories where user.name/user.email are unset.
func gitIn(dir string, stdin io.Reader, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdin = stdin
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=wasa",
		"GIT_AUTHOR_EMAIL=wasa@localhost",
		"GIT_COMMITTER_NAME=wasa",
		"GIT_COMMITTER_EMAIL=wasa@localhost",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf(
			"git %s: %w: %s", strings.Join(args, " "), err, msg,
		)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// headSHA returns the repository's current HEAD commit, or "" when there is
// none (empty repository) or dir is not a repository.
func headSHA(repoDir string) string {
	sha, _ := gitIn(
		repoDir,
		nil,
		"rev-parse",
		"--verify",
		"-q",
		"HEAD^{commit}",
	)
	return sha
}

// headBranch returns the branch HEAD is on, or "" when detached or unborn.
func headBranch(repoDir string) string {
	name, _ := gitIn(repoDir, nil, "rev-parse", "--abbrev-ref", "HEAD")
	if name == "HEAD" {
		return ""
	}
	return name
}

// commitsBetween lists the commits reachable from newHead but not from
// oldHead, oldest first. An empty oldHead lists everything up to newHead.
func commitsBetween(repoDir, oldHead, newHead string) []string {
	rng := newHead
	if oldHead != "" {
		rng = oldHead + ".." + newHead
	}
	out, err := gitIn(repoDir, nil, "rev-list", "--reverse", rng)
	if err != nil || out == "" {
		return nil
	}
	return strings.Fields(out)
}
