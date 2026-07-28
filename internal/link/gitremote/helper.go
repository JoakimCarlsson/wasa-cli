//go:build !windows

package gitremote

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// transport is the git subcommand the conversation is delegated to. It is
// spelled as a subcommand rather than as the git-remote-https binary directly
// so git resolves it out of its own exec-path, wherever that is on this host.
var transport = []string{"remote-https"}

// ExitError reports that the transport ran and failed with a status of its own.
//
// It deliberately carries no explanation: git has already written one to the
// terminal — the core's own wording, in most cases — and a second line from
// wasa on top of it says nothing the reader does not have. A caller turns this
// into an exit status and prints nothing.
type ExitError struct {
	// Code is the status the transport exited with.
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("the git transport exited with status %d", e.Code)
}

// Options is one remote-helper conversation.
type Options struct {
	// Remote is the nickname git invoked the helper for. It is empty when the
	// remote is an unnamed URL, which git allows.
	Remote string
	// Target is the core endpoint and token audience the URL resolved to.
	Target Target
	// Credential is the credential.helper value the child is configured with:
	// the command git runs to obtain a repo-scoped token. Build it with
	// CredentialConfig over Action's path — the two are one mechanism, and a
	// credential helper pointed at some other file can never be answered.
	Credential string
	// Action is where the direction is recorded for that credential helper to
	// read. The caller owns it because it has to name the same file in
	// Credential.
	Action *ActionFile
	// Stdin, Stdout and Stderr are git's side of the conversation. Stdin and
	// Stdout carry the remote-helper protocol and must not be buffered
	// elsewhere; Stderr is where the child's diagnostics reach the terminal.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Run delegates one remote-helper conversation to git's smart-HTTP transport
// against the core, minting the credential for it along the way.
//
// git's side of the stream is forwarded verbatim, so every capability and
// option the transport supports is answered by the transport itself. The one
// thing Run reads out of the stream is the direction, which it records before
// passing the command on — the child cannot have made a request yet, so the
// credential helper always finds it.
//
// Run ends when the transport does, not when git stops talking. git holds the
// helper's stdin open for as long as it is waiting for an answer, so reading
// that stream is not something a failed transfer ever interrupts: waiting on
// the transport instead is what makes a refused push exit rather than hang.
func Run(ctx context.Context, o Options) error {
	switch {
	case o.Target.URL == "" || o.Target.Audience == "":
		return errors.New("gitremote: unresolved remote")
	case o.Credential == "" || o.Action == nil:
		return errors.New("gitremote: no credential helper configured")
	case o.Stdin == nil || o.Stdout == nil:
		return errors.New("gitremote: no conversation to carry")
	}

	args := append([]string{}, transport...)
	if o.Remote != "" {
		args = append(args, o.Remote)
	}
	args = append(args, o.Target.URL)

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = childEnv(os.Environ(), o.Credential)
	cmd.Stderr = o.Stderr

	toChild, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("gitremote: %w", err)
	}
	fromChild, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("gitremote: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("gitremote: start the git transport: %w", err)
	}

	relayed := make(chan error, 1)
	go func() {
		_, err := io.Copy(o.Stdout, fromChild)
		relayed <- err
	}()

	forwarded := make(chan error, 1)
	go func() {
		err := forward(bufio.NewReader(o.Stdin), toChild, o.Action)
		_ = toChild.Close()
		forwarded <- err
	}()

	relayErr := <-relayed
	if err := cmd.Wait(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return &ExitError{Code: exit.ExitCode()}
		}
		return fmt.Errorf("gitremote: %s: %w", o.Target.URL, err)
	}

	var forwardErr error
	select {
	case forwardErr = <-forwarded:
	default:
	}
	return errors.Join(forwardErr, relayErr)
}

// forward pipes git's half of the conversation to the transport, recording the
// direction as soon as a command reveals it.
//
// Everything up to that command is newline-terminated text. After it the stream
// can turn into raw pkt-line — protocol v2 negotiates inside a
// stateless-connect — so the remainder is copied through without being read as
// lines. The reader is the one handed to io.Copy, so bytes already buffered
// behind the last line are not lost.
func forward(r *bufio.Reader, w io.Writer, action *ActionFile) error {
	for {
		line, readErr := r.ReadString('\n')
		if line != "" {
			direction, revealed := directionOf(line)
			if revealed {
				if err := action.Write(direction); err != nil {
					return err
				}
			}
			if _, err := io.WriteString(w, line); err != nil {
				return writeErr(err)
			}
			if revealed {
				if _, err := io.Copy(w, r); err != nil {
					return writeErr(err)
				}
				return nil
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return fmt.Errorf("gitremote: read git's request: %w", readErr)
		}
	}
}

// writeErr drops a closed pipe: the transport exiting first is how a failed
// transfer ends, and its own error is the one worth reporting.
func writeErr(err error) error {
	if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, os.ErrClosed) {
		return nil
	}
	return fmt.Errorf("gitremote: write to the git transport: %w", err)
}

// The environment entries the child's configuration is injected through. git
// reads a numbered list of key/value pairs from the environment, which keeps
// the credential helper out of any config file and out of the child's argv.
const (
	configCount = "GIT_CONFIG_COUNT"
	configKey   = "GIT_CONFIG_KEY_"
	configValue = "GIT_CONFIG_VALUE_"
	promptEnv   = "GIT_TERMINAL_PROMPT"
)

// childEnv builds the transport's environment: wasa's credential helper as the
// only one it may consult, and no terminal to prompt at.
//
// An inherited numbered list is extended rather than replaced, so config a
// caller injected the same way survives. The credential helper is reset to
// empty before wasa's is added, which clears the list git would otherwise have
// assembled from the user's config files — the token for this transfer comes
// from the core, and a stored credential for the core's host is not it.
func childEnv(env []string, credential string) []string {
	out := make([]string, 0, len(env)+6)
	count := 0
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		switch key {
		case configCount:
			if n, err := strconv.Atoi(value); err == nil && n > 0 {
				count = n
			}
		case promptEnv:
		default:
			out = append(out, entry)
		}
	}

	for _, pair := range [][2]string{
		{"credential.helper", ""},
		{"credential.helper", credential},
	} {
		out = append(out,
			fmt.Sprintf("%s%d=%s", configKey, count, pair[0]),
			fmt.Sprintf("%s%d=%s", configValue, count, pair[1]),
		)
		count++
	}
	return append(out,
		fmt.Sprintf("%s=%d", configCount, count),
		promptEnv+"=0",
	)
}

// CredentialConfig builds the credential.helper value pointing at wasa's own
// credential helper. exe is the wasa binary to invoke, coreURL the core the
// token is exchanged at, audience the repo the minted token is for, and
// actionPath the file the direction is recorded in.
//
// The core is passed down rather than re-derived because the helper may have
// read it off the remote: a token exchanged at some other core would not open
// this transfer's repo.
//
// The leading "!" is what makes git run the value as a command line rather than
// look for a git-credential-<name> binary, and every interpolated value is
// quoted because git hands that line to a shell.
func CredentialConfig(exe, coreURL, audience, actionPath string) string {
	return "!" + strings.Join([]string{
		shellQuote(exe),
		CredentialCommand,
		"--core", shellQuote(coreURL),
		"--audience", shellQuote(audience),
		"--action-file", shellQuote(actionPath),
	}, " ")
}

// shellQuote wraps a value so a shell reads it as one literal argument.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
