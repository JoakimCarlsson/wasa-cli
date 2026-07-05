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

// Write commits cp onto RefName in the repository containing repoDir (a main
// checkout or a linked worktree; the ref and objects live in the shared git
// dir either way). The transcript and the intent — which is usually lifted
// straight from the transcript — are redacted before they enter the object
// database. Only plumbing runs — hash-object, mktree, commit-tree,
// update-ref — so branches, index, working copy and reflog are untouched.
// The ref update is a compare-and-swap retried a few times, because
// concurrent sessions checkpoint the same ref.
func Write(repoDir string, cp Checkpoint) error {
	if cp.Meta.SessionID == "" {
		return fmt.Errorf("checkpoint has no session id")
	}
	metaJSON, err := json.MarshalIndent(cp.Meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
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
			return fmt.Errorf("hash %s: %w", f.name, err)
		}
		fmt.Fprintf(&tree, "100644 blob %s\t%s\n", sha, f.name)
	}
	treeSHA, err := gitIn(repoDir, strings.NewReader(tree.String()), "mktree")
	if err != nil {
		return fmt.Errorf("mktree: %w", err)
	}

	for range 3 {
		parent, _ := gitIn(
			repoDir, nil, "rev-parse", "--verify", "-q", RefName+"^{commit}",
		)
		args := []string{"commit-tree", treeSHA, "-m", cp.Meta.SessionID}
		if parent != "" {
			args = append(args, "-p", parent)
		}
		commit, err := gitIn(repoDir, nil, args...)
		if err != nil {
			return fmt.Errorf("commit-tree: %w", err)
		}
		args = []string{"update-ref", RefName, commit}
		if parent != "" {
			args = append(args, parent)
		}
		if _, err := gitIn(repoDir, nil, args...); err == nil {
			return nil
		}
	}
	return fmt.Errorf("update %s: lost the ref race repeatedly", RefName)
}

// Push best-effort syncs the checkpoint ref to origin. Offline, no origin or
// no permission are all expected outcomes; the caller decides whether the
// returned error is worth one log line. Credential prompts are disabled —
// terminal and GUI alike — so an unauthenticated push fails fast instead of
// hanging a hook invocation on a prompt nobody can see.
func Push(repoDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
	defer cancel()
	cmd := exec.CommandContext(
		ctx, "git", "-C", repoDir, "push", "origin", RefName+":"+RefName,
	)
	cmd.Env = pushEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"push %s: %w: %s", RefName, err,
			strings.TrimSpace(string(out)),
		)
	}
	return nil
}

// pushDetached fire-and-forgets the sync from hook context: the hook process
// must exit immediately (an agent cancels slow hooks and surfaces the noise),
// so the push runs in its own session and outlives it. No timeout: prompts
// are disabled, so git either finishes or fails on its own.
func pushDetached(repoDir string) {
	cmd := exec.Command(
		"git", "-C", repoDir, "push", "origin", RefName+":"+RefName,
	)
	cmd.Env = pushEnv()
	detach(cmd)
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
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
