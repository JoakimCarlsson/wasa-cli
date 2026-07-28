package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/joakimcarlsson/wasa-cli/internal/link/gitremote"
)

func TestRunArgvDispatchesOnTheHelperName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runArgv(
		"1.2.3",
		[]string{
			"/usr/local/bin/" + gitremote.BinaryName,
			"origin",
			"wasa://a/b",
		},
		&stdout,
		&stderr,
	)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (no login in a test home)", code)
	}
	if !strings.Contains(stderr.String(), "not logged in") {
		t.Errorf("stderr = %q, want the not-logged-in cure", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func TestRunRemoteHelperRunsTheHelper(t *testing.T) {
	var stderr bytes.Buffer
	code := runRemoteHelper([]string{"origin", "wasa://a/b"}, &stderr)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (no login in a test home)", code)
	}
	if !strings.Contains(stderr.String(), "not logged in") {
		t.Errorf("stderr = %q, want the not-logged-in cure", stderr.String())
	}
}

// git spawns the credential helper out of os.Executable(), which for the
// git-remote-wasa binary is that binary itself: it has to answer to the
// credential command as its first argument the way wasa does as a subcommand.
func TestRunRemoteHelperDispatchesTheCredentialCommand(t *testing.T) {
	var stderr bytes.Buffer
	code := runRemoteHelper([]string{gitremote.CredentialCommand}, &stderr)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), gitCredentialUsage) {
		t.Errorf(
			"stderr = %q, want the credential usage — the argument reached "+
				"the remote helper instead",
			stderr.String(),
		)
	}
}

func TestRunArgvKeepsOrdinarySubcommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runArgv(
		"1.2.3", []string{"/usr/local/bin/wasa", "--version"}, &stdout, &stderr,
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := strings.TrimSpace(stdout.String()); got != "wasa version 1.2.3" {
		t.Errorf("stdout = %q", got)
	}
}

func TestGitRemoteHelperRejectsWrongArity(t *testing.T) {
	for _, args := range [][]string{{}, {"a", "b", "c"}} {
		if err := runGitRemoteHelper(args); err == nil {
			t.Errorf("runGitRemoteHelper(%q): want an error", args)
		}
	}
}

func TestGitCredentialNeedsExactlyOneOperation(t *testing.T) {
	for _, args := range [][]string{
		{"--audience", "/et/a/b"},
		{"--audience", "/et/a/b", "get", "extra"},
	} {
		if err := runGitCredential(args); err == nil {
			t.Errorf("runGitCredential(%q): want an error", args)
		}
	}
}
