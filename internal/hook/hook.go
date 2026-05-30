// Package hook runs a profile's post-worktree hook: a shell command executed
// once, immediately after a worktree is created for a session, with its working
// directory set to that worktree and the same environment the session's agent
// will see. It is the seam that turns a freshly created but bare worktree into
// one that can build and run — installing dependencies, materializing a .env,
// warming caches.
//
// Execution is split behind a Runner interface so the command-building and
// failure-surfacing logic is unit-testable without spawning a process. A
// non-zero exit is reported as an *Error carrying the exit code and the tail of
// the hook's captured output. A worktree whose hook failed is deliberately left
// on disk rather than rolled back, so the failure can be diagnosed against the
// real tree; callers must treat the session as not launchable.
package hook

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Environment variables exported into every hook invocation, on top of the
// profile's own environment.
const (
	EnvRepoPath     = "WASA_REPO_PATH"
	EnvWorktreePath = "WASA_WORKTREE_PATH"
	EnvBranch       = "WASA_BRANCH"
	EnvSession      = "WASA_SESSION"
)

// maxTailLines bounds how many trailing lines of a stream are quoted in a
// hook's failure message, so a chatty hook does not bury the error.
const maxTailLines = 20

// Hook describes a single post-worktree hook invocation.
type Hook struct {
	// Command is the shell command to run. An empty command is a no-op.
	Command string
	// RepoPath is the workspace's canonical repository path, exported as
	// WASA_REPO_PATH.
	RepoPath string
	// WorktreePath is the newly created worktree. It is the hook's working
	// directory and is exported as WASA_WORKTREE_PATH.
	WorktreePath string
	// Branch is the branch the worktree is on, exported as WASA_BRANCH.
	Branch string
	// Session is the session identifier, exported as WASA_SESSION.
	Session string
	// Env is the profile's resolved environment as KEY=VALUE entries, layered
	// beneath the WASA_* variables so the hook sees what the agent will.
	Env []string
}

// Outcome is the captured result of a hook command that ran to completion,
// whatever its exit status.
type Outcome struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Runner executes command in dir with env (KEY=VALUE entries) layered onto the
// ambient environment, returning the captured outcome. A non-zero exit is
// reported through Outcome.ExitCode, not as an error; the returned error is
// reserved for a failure to start or wait for the process.
type Runner interface {
	Run(dir string, env []string, command string) (Outcome, error)
}

// Run executes h via r. An empty h.Command is a successful no-op. The hook runs
// with its working directory set to h.WorktreePath and with WASA_REPO_PATH,
// WASA_WORKTREE_PATH, WASA_BRANCH and WASA_SESSION exported on top of h.Env. A
// non-zero exit returns an *Error and leaves the worktree in place for
// inspection.
func Run(r Runner, h Hook) error {
	if h.Command == "" {
		return nil
	}

	out, err := r.Run(h.WorktreePath, h.environ(), h.Command)
	if err != nil {
		return fmt.Errorf("post-worktree hook: %w", err)
	}
	if out.ExitCode != 0 {
		return &Error{
			Command:  h.Command,
			ExitCode: out.ExitCode,
			Stdout:   out.Stdout,
			Stderr:   out.Stderr,
		}
	}
	return nil
}

// environ returns the hook's environment: the profile's own entries first, then
// the WASA_* variables, so the latter win on any key collision.
func (h Hook) environ() []string {
	env := make([]string, 0, len(h.Env)+4)
	env = append(env, h.Env...)
	return append(env,
		EnvRepoPath+"="+h.RepoPath,
		EnvWorktreePath+"="+h.WorktreePath,
		EnvBranch+"="+h.Branch,
		EnvSession+"="+h.Session,
	)
}

// Error reports a post-worktree hook that ran but exited non-zero. It carries
// the exit code and the hook's captured output so the CLI can surface the
// failure prominently. A worktree whose hook returned an Error is left on disk
// for inspection rather than rolled back.
type Error struct {
	Command  string
	ExitCode int
	Stdout   string
	Stderr   string
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(
		&b,
		"post-worktree hook %q failed (exit %d); worktree left in place for inspection",
		e.Command,
		e.ExitCode,
	)
	if tail := outputTail(e.Stderr); tail != "" {
		fmt.Fprintf(&b, "\nstderr:\n%s", tail)
	}
	if tail := outputTail(e.Stdout); tail != "" {
		fmt.Fprintf(&b, "\nstdout:\n%s", tail)
	}
	return b.String()
}

// outputTail returns the last maxTailLines non-empty-trimmed lines of s, or the
// empty string when s holds nothing but whitespace.
func outputTail(s string) string {
	s = strings.TrimRight(s, "\n")
	if strings.TrimSpace(s) == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > maxTailLines {
		lines = lines[len(lines)-maxTailLines:]
	}
	return strings.Join(lines, "\n")
}

// ShellRunner is the default Runner: it runs each hook command through a shell
// so pipelines and && compose as written. The zero value invokes "sh -c"; set
// Shell to use a different shell binary.
type ShellRunner struct {
	// Shell is the shell binary to invoke. Empty means "sh".
	Shell string
}

// Run executes command as `<shell> -c command` in dir, with env layered onto
// the process's ambient environment so PATH and the like remain available. A
// non-zero exit is returned via Outcome.ExitCode with a nil error; only a
// failure to start the shell yields a non-nil error.
func (s ShellRunner) Run(
	dir string,
	env []string,
	command string,
) (Outcome, error) {
	cmd := exec.Command(s.shell(), "-c", command)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	out := Outcome{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			out.ExitCode = exitErr.ExitCode()
			return out, nil
		}
		return out, fmt.Errorf("run %s -c: %w", s.shell(), err)
	}
	return out, nil
}

func (s ShellRunner) shell() string {
	if s.Shell == "" {
		return "sh"
	}
	return s.Shell
}
