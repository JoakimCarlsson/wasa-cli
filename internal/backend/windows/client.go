//go:build windows

package conpty

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// DefaultProgram is the program a spawned session runs when the caller names
// none. The native backend's bare-session default is cmd.exe, the Windows
// equivalent of the tmux backend's bash.
const DefaultProgram = "cmd"

// Hidden wasa subcommands the client shells out to. DaemonSubcommand runs the
// background daemon; AttachSubcommand runs the per-attach relay client that
// AttachCmd points tea.ExecProcess at.
const (
	DaemonSubcommand = "__daemon"
	AttachSubcommand = "__attach"
)

// Client is the native Windows session backend. It is a thin RPC client over
// the per-user named pipe: every operation dials the daemon, and a spawn that
// finds no daemon auto-starts one. It satisfies backend.SessionBackend.
type Client struct{}

// New returns a native Windows session backend.
func New() *Client { return &Client{} }

// SpawnEnv creates a detached session named name running program (cmd.exe when
// none is given) in dir with each KEY=VALUE entry of env injected. dir is
// resolved to an absolute path against the caller's working directory because
// the daemon runs elsewhere. The daemon is auto-started if it is not running.
func (c *Client) SpawnEnv(
	name, dir string,
	env []string,
	program ...string,
) error {
	if !supported() {
		return ErrUnsupported
	}
	if err := validateName(name); err != nil {
		return err
	}
	if len(program) == 0 {
		program = []string{DefaultProgram}
	}
	if dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return fmt.Errorf("conpty: resolve dir %q: %w", dir, err)
		}
		dir = abs
	}

	conn, err := dialOrStart()
	if err != nil {
		return err
	}
	_, err = roundtrip(conn, request{
		Op:      opSpawn,
		Name:    name,
		Dir:     dir,
		Env:     env,
		Program: program,
	})
	return err
}

// AttachCmd returns the unstarted command that attaches to name by running the
// hidden attach subcommand against the current executable. The TUI hands this to
// tea.ExecProcess, and the CLI wires it to the real terminal; either way the
// attach client owns the terminal and relays it to the session.
func (c *Client) AttachCmd(name string) (*exec.Cmd, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("conpty: locate wasa executable: %w", err)
	}
	return exec.Command(exe, AttachSubcommand, name), nil
}

// Capture returns the session's visible screen as plain text, or an empty
// string when the session (or the daemon) is gone.
func (c *Client) Capture(name string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	conn, err := dialPipe()
	if err != nil || conn == nil {
		return "", err
	}
	resp, err := roundtrip(conn, request{Op: opCapture, Name: name})
	if err != nil {
		return "", err
	}
	return resp.Capture, nil
}

// Has reports whether the session exists. No daemon means no sessions.
func (c *Client) Has(name string) (bool, error) {
	if err := validateName(name); err != nil {
		return false, err
	}
	conn, err := dialPipe()
	if err != nil || conn == nil {
		return false, err
	}
	resp, err := roundtrip(conn, request{Op: opHas, Name: name})
	if err != nil {
		return false, err
	}
	return resp.Exists, nil
}

// List returns the live session names. No daemon means no sessions.
func (c *Client) List() ([]string, error) {
	conn, err := dialPipe()
	if err != nil || conn == nil {
		return nil, err
	}
	resp, err := roundtrip(conn, request{Op: opList})
	if err != nil {
		return nil, err
	}
	return resp.Names, nil
}

// Kill terminates the session. Killing an unknown session, or with no daemon
// running, is a no-op rather than an error, matching the tmux backend.
func (c *Client) Kill(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	conn, err := dialPipe()
	if err != nil || conn == nil {
		return err
	}
	_, err = roundtrip(conn, request{Op: opKill, Name: name})
	return err
}

// roundtrip sends one request and reads one response over conn, closing it. A
// response carrying Err is turned into a Go error.
func roundtrip(conn *pipeConn, req request) (response, error) {
	defer conn.Close()
	if err := writeFrame(conn, req); err != nil {
		return response{}, err
	}
	var resp response
	if err := readFrame(conn, &resp); err != nil {
		return response{}, err
	}
	if resp.Err != "" {
		return resp, errors.New(resp.Err)
	}
	return resp, nil
}

// dialOrStart connects to the daemon, auto-starting it if no daemon is running.
func dialOrStart() (*pipeConn, error) {
	conn, err := dialPipe()
	if err != nil {
		return nil, err
	}
	if conn != nil {
		return conn, nil
	}
	if err := startDaemon(); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := dialPipe()
		if err != nil {
			return nil, err
		}
		if conn != nil {
			return conn, nil
		}
		time.Sleep(30 * time.Millisecond)
	}
	return nil, errors.New("conpty: timed out waiting for session daemon")
}

// startDaemon launches the daemon subcommand as a detached, window-less
// background process that survives this wasa invocation.
func startDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("conpty: locate wasa executable: %w", err)
	}
	cmd := exec.Command(exe, DaemonSubcommand)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NO_WINDOW,
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("conpty: start session daemon: %w", err)
	}
	return cmd.Process.Release()
}

// validateName mirrors the tmux backend's rules so a session name accepted by
// one backend is accepted by the other and the registry stays backend-agnostic.
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
