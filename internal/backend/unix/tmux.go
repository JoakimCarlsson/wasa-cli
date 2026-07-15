//go:build !windows

package tmux

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// DefaultProgram is the program a spawned session runs when the caller does not
// specify one. It is an interactive shell suitable for the demo subcommand.
const DefaultProgram = "bash"

// Client drives a tmux server by shelling out to the tmux binary. The zero
// value invokes "tmux" on PATH; prefer New.
type Client struct {
	// Bin is the tmux executable to invoke. Empty means "tmux" on PATH.
	Bin string
}

// New returns a Client that invokes tmux from PATH.
func New() *Client {
	return &Client{Bin: "tmux"}
}

// Spawn creates a detached session named name that runs program (or an
// interactive shell when program is empty) with working directory dir.
func (c *Client) Spawn(name, dir string, program ...string) error {
	return c.SpawnEnv(name, dir, nil, program...)
}

// SpawnEnv is Spawn with environment injection: each entry of env, in KEY=VALUE
// form, is passed as a tmux new-session -e argument so the variable lives on the
// session and survives on the shared tmux server.
//
// After the session is created it is switched to remain-on-exit so the pane
// lingers as a dead pane when its program exits, holding the exit status
// PaneExit reads to tell a finished session from a failed one. The session
// therefore outlives its program until it is killed explicitly (finish, pause,
// delete); reconcile treats a dead pane as exited.
func (c *Client) SpawnEnv(
	name, dir string,
	env []string,
	program ...string,
) error {
	if err := validateName(name); err != nil {
		return err
	}
	if _, err := c.run(spawnArgs(name, dir, env, program)...); err != nil {
		return err
	}
	_, err := c.run(remainOnExitArgs(name)...)
	return err
}

// AttachCmd returns the unstarted command that attaches to the session named
// name, with no standard streams wired. It is the seam the TUI hands to
// tea.ExecProcess, which must own the terminal for the duration of the attach:
// Bubble Tea suspends its renderer, runs this command with the real terminal
// stdin/stdout/stderr, and resumes on detach (C-b d). Running an attach command
// whose streams the caller wired by hand from inside a live Bubble Tea program
// corrupts the TUI, which is why the TUI never calls Attach directly.
//
// The command's environment has $TMUX cleared so the attach succeeds even when
// wasa itself was launched from inside a tmux session, where tmux's nested-
// session guard would otherwise refuse to attach.
func (c *Client) AttachCmd(name string) (*exec.Cmd, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	cmd := exec.Command(c.bin(), attachArgs(name)...)
	cmd.Env = envWithout(os.Environ(), "TMUX")
	return cmd, nil
}

// envWithout returns environ with every KEY=VALUE entry whose key is key
// removed. tmux exports both TMUX and TMUX_PANE; clearing TMUX alone is enough
// to lift the nested-session guard.
func envWithout(environ []string, key string) []string {
	prefix := key + "="
	out := environ[:0:0]
	for _, e := range environ {
		if strings.HasPrefix(e, prefix) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// Attach hands the current terminal to tmux, attaching to the session named
// name. It wires the process's standard streams to tmux and blocks until tmux
// exits, for example when the user detaches with C-b d. This is the CLI path;
// the TUI attaches through AttachCmd and tea.ExecProcess.
func (c *Client) Attach(name string) error {
	cmd, err := c.AttachCmd(name)
	if err != nil {
		return err
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return notInstalled(err)
		}
		return fmt.Errorf(
			"tmux %s: %w",
			strings.Join(attachArgs(name), " "),
			err,
		)
	}
	return nil
}

// Has reports whether a session named name exists. A missing session is not an
// error; a missing tmux binary is.
func (c *Client) Has(name string) (bool, error) {
	if err := validateName(name); err != nil {
		return false, err
	}

	_, _, err := c.output(hasArgs(name)...)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, exec.ErrNotFound):
		return false, notInstalled(err)
	default:
		if _, ok := errors.AsType[*exec.ExitError](err); ok {
			return false, nil
		}
		return false, fmt.Errorf("tmux has-session: %w", err)
	}
}

// PaneExit reports the liveness of the session named name and, when its program
// has exited under remain-on-exit, that program's exit code. alive is true while
// the program still runs; once the pane is dead alive is false and exitCode is
// the program's status when tmux recorded one — nil for a session killed outright
// (no pane to read) or a pane that died on a signal (no numeric status). A missing
// session is not an error: it reports (false, nil, nil), the same as a session
// torn down without a captured code. A missing tmux binary is an error.
func (c *Client) PaneExit(name string) (alive bool, exitCode *int, err error) {
	if err := validateName(name); err != nil {
		return false, nil, err
	}

	stdout, _, runErr := c.output(paneExitArgs(name)...)
	switch {
	case runErr == nil:
		alive, code := parsePaneExit(stdout)
		return alive, code, nil
	case errors.Is(runErr, exec.ErrNotFound):
		return false, nil, notInstalled(runErr)
	default:
		if _, ok := errors.AsType[*exec.ExitError](runErr); ok {
			return false, nil, nil
		}
		return false, nil, fmt.Errorf("tmux list-panes: %w", runErr)
	}
}

// List returns the names of the sessions on the server. When no server is
// running it returns an empty list rather than an error.
func (c *Client) List() ([]string, error) {
	stdout, stderr, err := c.output(listArgs()...)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, notInstalled(err)
		}
		if _, ok := errors.AsType[*exec.ExitError](err); ok &&
			noServer(stderr) {
			return nil, nil
		}
		if msg := strings.TrimSpace(stderr); msg != "" {
			return nil, fmt.Errorf("tmux list-sessions: %w: %s", err, msg)
		}
		return nil, fmt.Errorf("tmux list-sessions: %w", err)
	}
	return parseSessions(stdout), nil
}

// Capture returns the visible contents of the active pane of the session named
// name, with the pane's escape sequences preserved (see captureArgs), for
// rendering a read-only preview that keeps the agent's colors. A session that
// does not exist yields an empty string rather than an error, so a just-exited
// session degrades to a blank preview instead of a hard failure.
func (c *Client) Capture(name string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}

	stdout, _, err := c.output(captureArgs(name)...)
	switch {
	case err == nil:
		return stdout, nil
	case errors.Is(err, exec.ErrNotFound):
		return "", notInstalled(err)
	default:
		if _, ok := errors.AsType[*exec.ExitError](err); ok {
			return "", nil
		}
		return "", fmt.Errorf("tmux capture-pane: %w", err)
	}
}

// Kill kills the session named name.
func (c *Client) Kill(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	_, err := c.run(killArgs(name)...)
	return err
}

func spawnArgs(name, dir string, env, program []string) []string {
	args := []string{"new-session", "-d", "-s", name}
	for _, e := range env {
		args = append(args, "-e", e)
	}
	if dir != "" {
		args = append(args, "-c", dir)
	}
	if len(program) == 0 {
		program = []string{DefaultProgram}
	}
	return append(args, program...)
}

func attachArgs(name string) []string {
	return []string{"attach-session", "-t", name}
}

func hasArgs(name string) []string {
	return []string{"has-session", "-t", name}
}

// remainOnExitArgs keeps a session's window open after its program exits, so the
// pane becomes dead rather than closing the session. It targets the session's
// window (-w) rather than the server, leaving the user's other tmux sessions
// untouched.
func remainOnExitArgs(name string) []string {
	return []string{"set-option", "-t", name, "-w", "remain-on-exit", "on"}
}

// paneExitArgs asks for the death state and exit status of a session's pane.
// pane_dead is 1 for a dead pane, and pane_dead_status carries its program's
// exit code when tmux recorded one.
func paneExitArgs(name string) []string {
	return []string{
		"list-panes", "-t", name, "-F", "#{pane_dead} #{pane_dead_status}",
	}
}

func killArgs(name string) []string {
	return []string{"kill-session", "-t", name}
}

// captureArgs builds the capture-pane invocation. -e preserves the pane's
// escape sequences so the preview keeps the agent's colors; without it tmux
// strips all SGR and the capture is flat monochrome. -p writes to stdout. -J
// joins a line tmux soft-wrapped at the pane's own width back into one logical
// line; without it a source line wider than the pane comes back as several
// physical rows, which the render path (internal/tui) would then treat as
// that many separate lines instead of one it can truncate by visible width.
func captureArgs(name string) []string {
	return []string{"capture-pane", "-e", "-J", "-p", "-t", name}
}

func listArgs() []string {
	return []string{"list-sessions", "-F", "#{session_name}"}
}

// validateName rejects session names tmux cannot address. tmux treats ':' and
// '.' as window and pane separators in target specifiers, so a name containing
// either cannot be reliably targeted; an empty name is also rejected.
func validateName(name string) error {
	if name == "" {
		return errors.New("session name must not be empty")
	}
	if strings.ContainsAny(name, ":.") {
		return fmt.Errorf(
			"invalid session name %q: must not contain ':' or '.'",
			name,
		)
	}
	return nil
}

// parsePaneExit reads the first "#{pane_dead} #{pane_dead_status}" line
// list-panes prints. A leading "1" marks a dead pane; a numeric second field is
// its program's exit code. A live pane (leading "0") reports alive with no code,
// and a dead pane with no numeric status (a signal death) reports exited with no
// code. Empty output — a session with no readable pane — reports exited too.
func parsePaneExit(stdout string) (alive bool, exitCode *int) {
	var line string
	for l := range strings.SplitSeq(stdout, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			line = l
			break
		}
	}
	if line == "" {
		return false, nil
	}
	fields := strings.Fields(line)
	if fields[0] != "1" {
		return true, nil
	}
	if len(fields) >= 2 {
		if code, err := strconv.Atoi(fields[1]); err == nil {
			return false, &code
		}
	}
	return false, nil
}

func parseSessions(stdout string) []string {
	var names []string
	for line := range strings.SplitSeq(stdout, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, line)
		}
	}
	return names
}

func noServer(stderr string) bool {
	return strings.Contains(stderr, "no server running")
}

// run executes tmux with args, returning stdout. On failure it surfaces tmux's
// stderr and maps a missing binary to an actionable error.
func (c *Client) run(args ...string) (string, error) {
	stdout, stderr, err := c.output(args...)
	if err == nil {
		return stdout, nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return "", notInstalled(err)
	}
	if msg := strings.TrimSpace(stderr); msg != "" {
		return "", fmt.Errorf(
			"tmux %s: %w: %s",
			strings.Join(args, " "),
			err,
			msg,
		)
	}
	return "", fmt.Errorf("tmux %s: %w", strings.Join(args, " "), err)
}

func (c *Client) output(args ...string) (stdout, stderr string, err error) {
	cmd := exec.Command(c.bin(), args...)

	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se

	err = cmd.Run()
	return so.String(), se.String(), err
}

func (c *Client) bin() string {
	if c.Bin == "" {
		return "tmux"
	}
	return c.Bin
}

func notInstalled(err error) error {
	return fmt.Errorf(
		"tmux binary not found on PATH: install tmux and try again: %w",
		err,
	)
}
