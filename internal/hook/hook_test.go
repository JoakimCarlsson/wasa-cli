package hook

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// recordingRunner captures the arguments of the single Run call it expects and
// returns a configured outcome, so command-building and failure handling are
// asserted without spawning a process.
type recordingRunner struct {
	outcome Outcome
	err     error

	calls   int
	dir     string
	env     []string
	command string
}

func (r *recordingRunner) Run(
	dir string,
	env []string,
	command string,
) (Outcome, error) {
	r.calls++
	r.dir = dir
	r.env = env
	r.command = command
	return r.outcome, r.err
}

func TestRunExportsEnvAndCwd(t *testing.T) {
	r := &recordingRunner{}
	h := Hook{
		Command:      "true",
		RepoPath:     "/repo",
		WorktreePath: "/wt",
		Branch:       "feature/x",
		Session:      "abc123",
		Env:          []string{"FOO=bar", "BAZ=qux"},
	}

	if err := Run(r, h); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if r.dir != "/wt" {
		t.Fatalf("cwd = %q, want /wt", r.dir)
	}
	if r.command != "true" {
		t.Fatalf("command = %q, want true", r.command)
	}

	want := map[string]string{
		"FOO":           "bar",
		"BAZ":           "qux",
		EnvRepoPath:     "/repo",
		EnvWorktreePath: "/wt",
		EnvBranch:       "feature/x",
		EnvSession:      "abc123",
	}
	got := envMap(r.env)
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("env[%q] = %q, want %q (env=%v)", k, got[k], v, r.env)
		}
	}
}

func TestRunWasaVarsWinOverProfileEnv(t *testing.T) {
	r := &recordingRunner{}
	h := Hook{
		Command:      "true",
		RepoPath:     "/repo",
		WorktreePath: "/wt",
		Branch:       "main",
		Session:      "s1",
		Env:          []string{EnvBranch + "=stale"},
	}

	if err := Run(r, h); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if idx := slices.Index(r.env, EnvBranch+"=stale"); idx == -1 {
		t.Fatalf("expected profile entry to be present, env=%v", r.env)
	}
	last := lastValue(r.env, EnvBranch)
	if last != "main" {
		t.Fatalf("last %s = %q, want main", EnvBranch, last)
	}
}

func TestRunEmptyCommandIsNoop(t *testing.T) {
	r := &recordingRunner{}
	if err := Run(r, Hook{WorktreePath: "/wt"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.calls != 0 {
		t.Fatalf("runner called %d times, want 0", r.calls)
	}
}

func TestRunNonZeroExitSurfacesError(t *testing.T) {
	r := &recordingRunner{
		outcome: Outcome{
			ExitCode: 3,
			Stdout:   "building...\n",
			Stderr:   "boom: missing dep\n",
		},
	}
	h := Hook{Command: "exit 3", WorktreePath: "/wt"}

	err := Run(r, h)
	if err == nil {
		t.Fatal("expected error on non-zero exit, got nil")
	}

	herr, ok := errors.AsType[*Error](err)
	if !ok {
		t.Fatalf("error is not *Error: %T", err)
	}
	if herr.ExitCode != 3 {
		t.Fatalf("exit code = %d, want 3", herr.ExitCode)
	}

	msg := err.Error()
	for _, want := range []string{"exit 3", "boom: missing dep", "building..."} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}
	if !strings.Contains(msg, "left in place") {
		t.Fatalf("error %q does not state the worktree is left in place", msg)
	}
}

func TestRunStartFailureIsWrapped(t *testing.T) {
	r := &recordingRunner{err: errors.New("sh not found")}
	err := Run(r, Hook{Command: "true", WorktreePath: "/wt"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if herr, ok := errors.AsType[*Error](err); ok {
		t.Fatalf("start failure should not be *Error, got %v", herr)
	}
	if !strings.Contains(err.Error(), "sh not found") {
		t.Fatalf("error %q missing cause", err.Error())
	}
}

func TestShellRunnerRunsRealHook(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available on PATH")
	}

	dir := t.TempDir()
	out, err := ShellRunner{}.Run(
		dir,
		[]string{EnvBranch + "=demo"},
		"echo hi > wasa-hook-ran && printf '%s' \"$"+EnvBranch+"\" >> wasa-hook-ran",
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr=%q)", out.ExitCode, out.Stderr)
	}

	data, err := os.ReadFile(filepath.Join(dir, "wasa-hook-ran"))
	if err != nil {
		t.Fatalf("hook side effect not observable: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "hi\ndemo" {
		t.Fatalf("hook output = %q, want %q", got, "hi\ndemo")
	}
}

func TestShellRunnerReportsNonZeroExit(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available on PATH")
	}

	out, err := ShellRunner{}.Run(t.TempDir(), nil, "exit 3")
	if err != nil {
		t.Fatalf("Run returned error for non-zero exit: %v", err)
	}
	if out.ExitCode != 3 {
		t.Fatalf("exit code = %d, want 3", out.ExitCode)
	}
}

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok {
			m[k] = v
		}
	}
	return m
}

func lastValue(env []string, key string) string {
	val := ""
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok && k == key {
			val = v
		}
	}
	return val
}
