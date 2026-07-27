//go:build !windows

package gitremote

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joakimcarlsson/wasa-cli/internal/link/repotoken"
)

func TestForwardRecordsTheDirectionBeforePassingItOn(t *testing.T) {
	conversation := "capabilities\n" +
		"option progress false\n" +
		"stateless-connect git-receive-pack\n" +
		"0014command=push\x00raw"

	f, err := NewActionFile()
	if err != nil {
		t.Fatalf("NewActionFile: %v", err)
	}
	t.Cleanup(func() { _ = f.Remove() })

	var seen strings.Builder
	watched := &watchingWriter{
		w: &seen,
		on: func(p []byte) {
			if !strings.Contains(string(p), "stateless-connect") {
				return
			}
			if _, err := ReadAction(f.Path()); err != nil {
				t.Errorf("direction was not recorded first: %v", err)
			}
		},
	}

	err = forward(bufio.NewReader(strings.NewReader(conversation)), watched, f)
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	if seen.String() != conversation {
		t.Errorf("forwarded %q, want %q", seen.String(), conversation)
	}
	action, err := ReadAction(f.Path())
	if err != nil {
		t.Fatalf("ReadAction: %v", err)
	}
	if action != repotoken.Push {
		t.Errorf("action = %q, want %q", action, repotoken.Push)
	}
}

func TestForwardLeavesTheDirectionUnsetWhenNothingReveals(t *testing.T) {
	f, err := NewActionFile()
	if err != nil {
		t.Fatalf("NewActionFile: %v", err)
	}
	t.Cleanup(func() { _ = f.Remove() })

	var seen strings.Builder
	err = forward(
		bufio.NewReader(strings.NewReader("capabilities\n")),
		&seen,
		f,
	)
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	if _, err := ReadAction(f.Path()); err == nil {
		t.Error("a direction was recorded from capabilities alone")
	}
}

func TestChildEnvExtendsAnInheritedConfigList(t *testing.T) {
	env := childEnv([]string{
		"HOME=/home/dev",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.pager",
		"GIT_CONFIG_VALUE_0=cat",
		"GIT_TERMINAL_PROMPT=1",
	}, "!wasa cred")

	want := []string{
		"HOME=/home/dev",
		"GIT_CONFIG_KEY_0=core.pager",
		"GIT_CONFIG_VALUE_0=cat",
		"GIT_CONFIG_KEY_1=credential.helper",
		"GIT_CONFIG_VALUE_1=",
		"GIT_CONFIG_KEY_2=credential.helper",
		"GIT_CONFIG_VALUE_2=!wasa cred",
		"GIT_CONFIG_COUNT=3",
		"GIT_TERMINAL_PROMPT=0",
	}
	if !slices.Equal(env, want) {
		t.Errorf("childEnv =\n%q\nwant\n%q", env, want)
	}
}

func TestChildEnvResetsTheCredentialHelperFirst(t *testing.T) {
	env := childEnv([]string{"HOME=/home/dev"}, "!wasa cred")

	reset := slices.Index(env, "GIT_CONFIG_VALUE_0=")
	ours := slices.Index(env, "GIT_CONFIG_VALUE_1=!wasa cred")
	if reset < 0 || ours < 0 || reset > ours {
		t.Errorf("the helper list is not reset before wasa's is added: %q", env)
	}
}

func TestCredentialConfigQuotesEveryValue(t *testing.T) {
	got := CredentialConfig(
		"/opt/it's here/wasa", "/et/acme/widgets", "/tmp/x y/action",
	)
	want := "!'/opt/it'\\''s here/wasa' " + CredentialCommand +
		" --audience '/et/acme/widgets' --action-file '/tmp/x y/action'"
	if got != want {
		t.Errorf("CredentialConfig = %q, want %q", got, want)
	}
}

func TestRunRefusesAnIncompleteConversation(t *testing.T) {
	f, err := NewActionFile()
	if err != nil {
		t.Fatalf("NewActionFile: %v", err)
	}
	t.Cleanup(func() { _ = f.Remove() })

	target := Target{Audience: "/et/a/b", URL: "https://core.example/et/a/b"}
	tests := []Options{
		{
			Credential: "!x",
			Action:     f,
			Stdin:      strings.NewReader(""),
			Stdout:     io.Discard,
		},
		{
			Target: target,
			Action: f,
			Stdin:  strings.NewReader(""),
			Stdout: io.Discard,
		},
		{
			Target:     target,
			Credential: "!x",
			Stdin:      strings.NewReader(""),
			Stdout:     io.Discard,
		},
		{Target: target, Credential: "!x", Action: f, Stdout: io.Discard},
	}
	for i, o := range tests {
		if err := Run(context.Background(), o); err == nil {
			t.Errorf("Run(%d): want an error", i)
		}
	}
}

// TestRunRecordsTheDirectionInTheCallersFile pins the one thing the credential
// helper depends on: the file Run writes the direction to is the file the
// caller named in Credential. A Run that made its own would answer every
// request with "no direction recorded".
func TestRunRecordsTheDirectionInTheCallersFile(t *testing.T) {
	f, err := NewActionFile()
	if err != nil {
		t.Fatalf("NewActionFile: %v", err)
	}
	t.Cleanup(func() { _ = f.Remove() })

	target := Target{
		Audience: "/et/acme/widgets",
		URL:      "https://core.invalid/et/acme/widgets",
	}
	_ = Run(context.Background(), Options{
		Target: target,
		Action: f,
		Credential: CredentialConfig(
			"/nonexistent/wasa",
			target.Audience,
			f.Path(),
		),
		Stdin:  strings.NewReader("capabilities\nlist for-push\n"),
		Stdout: io.Discard,
		Stderr: io.Discard,
	})

	action, err := ReadAction(f.Path())
	if err != nil {
		t.Fatalf("ReadAction: %v", err)
	}
	if action != repotoken.Push {
		t.Errorf("action = %q, want %q", action, repotoken.Push)
	}
}

func TestRunReportsATransportThatFails(t *testing.T) {
	f, err := NewActionFile()
	if err != nil {
		t.Fatalf("NewActionFile: %v", err)
	}
	t.Cleanup(func() { _ = f.Remove() })

	err = Run(context.Background(), Options{
		Target: Target{
			Audience: "/et/acme/widgets",
			URL:      "https://core.invalid/et/acme/widgets",
		},
		Action: f,
		Credential: CredentialConfig(
			"/nonexistent/wasa", "/et/acme/widgets", f.Path(),
		),
		Stdin:  strings.NewReader("capabilities\nlist\n"),
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	var exit *ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("Run against an unreachable core = %v, want an ExitError", err)
	}
	if exit.Code <= 0 {
		t.Errorf("exit code = %d, want the transport's own status", exit.Code)
	}
}

// TestRunEndsWhenTheTransportDoesNotWhenGitStops reproduces the hang a real
// git parent produces: it keeps the helper's stdin open while it waits for an
// answer, so a Run that ends only on stdin's EOF never returns from a transfer
// the core refused.
func TestRunEndsWhenTheTransportDoesNotWhenGitStops(t *testing.T) {
	f, err := NewActionFile()
	if err != nil {
		t.Fatalf("NewActionFile: %v", err)
	}
	t.Cleanup(func() { _ = f.Remove() })

	held, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	go func() { _, _ = io.WriteString(writer, "capabilities\nlist\n") }()

	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), Options{
			Target: Target{
				Audience: "/et/acme/widgets",
				URL:      "https://core.invalid/et/acme/widgets",
			},
			Action: f,
			Credential: CredentialConfig(
				"/nonexistent/wasa", "/et/acme/widgets", f.Path(),
			),
			Stdin:  held,
			Stdout: io.Discard,
			Stderr: io.Discard,
		})
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Error("Run against an unreachable core = nil")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return once the transport had exited")
	}
}

func TestWriteErrDropsAClosedPipe(t *testing.T) {
	if err := writeErr(io.ErrClosedPipe); err != nil {
		t.Errorf("writeErr(ErrClosedPipe) = %v, want nil", err)
	}
	if err := writeErr(errors.New("disk on fire")); err == nil {
		t.Error("writeErr on a real failure = nil")
	}
}

// watchingWriter runs on before each write reaches w, so a test can assert on
// the state of the world at the moment a particular line is forwarded.
type watchingWriter struct {
	w  io.Writer
	on func([]byte)
}

func (ww *watchingWriter) Write(p []byte) (int, error) {
	ww.on(p)
	n, err := ww.w.Write(p)
	if err != nil {
		return n, fmt.Errorf("watchingWriter: %w", err)
	}
	return n, nil
}
