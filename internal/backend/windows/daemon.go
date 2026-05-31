//go:build windows

package conpty

import (
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

// idleGrace is how long the daemon lingers with no sessions before exiting. It
// is generous so routine list/has probes between bursts of work do not pay a
// cold daemon start, yet bounded so an abandoned daemon does not run forever.
const idleGrace = 10 * time.Minute

// Default pseudo-console size used when a spawn requests none. It is a plausible
// terminal so a program's first paint is reasonable before any attach resizes
// it to the real window.
const (
	defaultCols int16 = 120
	defaultRows int16 = 30
)

// daemon owns every live session and serves the per-user control pipe. It is
// the process that makes sessions outlive the wasa CLI/TUI: ConPTY handles die
// with their owning process, so that owner is this long-lived background daemon
// rather than a transient wasa invocation.
type daemon struct {
	mu        sync.Mutex
	sessions  map[string]*session
	idleTimer *time.Timer
}

func newDaemon() *daemon {
	d := &daemon{sessions: make(map[string]*session)}
	d.mu.Lock()
	d.armIdleLocked()
	d.mu.Unlock()
	return d
}

// RunDaemon runs the session daemon until it has had no sessions for idleGrace,
// then returns. A second daemon racing to own the control pipe loses the
// FILE_FLAG_FIRST_PIPE_INSTANCE claim and returns cleanly, so concurrent
// auto-starts collapse to a single daemon. It returns ErrUnsupported on hosts
// without the pseudo-console API.
func RunDaemon() error {
	if !supported() {
		return ErrUnsupported
	}
	d := newDaemon()
	first := true
	for {
		h, err := listenPipe(first)
		if err != nil {
			if first {
				return nil
			}
			return err
		}
		first = false
		if err := acceptPipe(h); err != nil {
			windows.CloseHandle(h)
			continue
		}
		go d.serve(&pipeConn{h: h})
	}
}

// serve handles one client connection: read its request frame, then either run
// a control operation and reply, or hand an attach request to the raw relay.
func (d *daemon) serve(conn *pipeConn) {
	var req request
	if err := readFrame(conn, &req); err != nil {
		conn.Close()
		return
	}
	if req.Op == opAttach {
		d.handleAttach(conn, req)
		return
	}
	resp := d.handle(req)
	_ = writeFrame(conn, resp)
	conn.Close()
}

func (d *daemon) handle(req request) response {
	switch req.Op {
	case opPing:
		return response{}
	case opSpawn:
		return d.spawn(req)
	case opCapture:
		if s := d.get(req.Name); s != nil {
			return response{Capture: s.Capture()}
		}
		return response{}
	case opHas:
		return response{Exists: d.get(req.Name) != nil}
	case opList:
		return response{Names: d.list()}
	case opKill:
		d.kill(req.Name)
		return response{}
	default:
		return response{Err: fmt.Sprintf("unknown op %q", req.Op)}
	}
}

func (d *daemon) handleAttach(conn *pipeConn, req request) {
	defer conn.Close()
	s := d.get(req.Name)
	if s == nil {
		_ = writeFrame(conn, response{
			Err: fmt.Sprintf("session %q does not exist", req.Name),
		})
		return
	}
	s.Resize(req.Cols, req.Rows)
	if err := writeFrame(conn, response{}); err != nil {
		return
	}
	s.stream(conn)
}

func (d *daemon) spawn(req request) response {
	if req.Name == "" {
		return response{Err: "session name must not be empty"}
	}
	if d.get(req.Name) != nil {
		return response{Err: fmt.Sprintf("session %q already exists", req.Name)}
	}

	cols, rows := req.Cols, req.Rows
	if cols <= 0 {
		cols = defaultCols
	}
	if rows <= 0 {
		rows = defaultRows
	}

	cpty, err := startConPty(cols, rows, req.Dir, req.Env, req.Program)
	if err != nil {
		return response{Err: err.Error()}
	}

	s := newSession(req.Name, req.Dir, req.Program, cols, rows, cpty, d.remove)
	d.mu.Lock()
	if _, exists := d.sessions[req.Name]; exists {
		d.mu.Unlock()
		s.cpty.Close()
		return response{Err: fmt.Sprintf("session %q already exists", req.Name)}
	}
	d.sessions[req.Name] = s
	d.disarmIdleLocked()
	d.mu.Unlock()
	return response{}
}

func (d *daemon) kill(name string) {
	d.mu.Lock()
	s := d.sessions[name]
	delete(d.sessions, name)
	d.armIdleLocked()
	d.mu.Unlock()
	if s != nil {
		s.cpty.Close()
	}
}

// remove drops a session that exited on its own (its program terminated). It is
// the onExit callback handed to each session.
func (d *daemon) remove(name string) {
	d.mu.Lock()
	delete(d.sessions, name)
	d.armIdleLocked()
	d.mu.Unlock()
}

func (d *daemon) get(name string) *session {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sessions[name]
}

func (d *daemon) list() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	names := make([]string, 0, len(d.sessions))
	for name := range d.sessions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// armIdleLocked starts (or restarts) the idle countdown when no sessions remain.
// It must be called with mu held.
func (d *daemon) armIdleLocked() {
	if len(d.sessions) != 0 {
		return
	}
	if d.idleTimer != nil {
		d.idleTimer.Stop()
	}
	d.idleTimer = time.AfterFunc(idleGrace, d.idleExit)
}

// disarmIdleLocked cancels the idle countdown. It must be called with mu held.
func (d *daemon) disarmIdleLocked() {
	if d.idleTimer != nil {
		d.idleTimer.Stop()
		d.idleTimer = nil
	}
}

func (d *daemon) idleExit() {
	d.mu.Lock()
	empty := len(d.sessions) == 0
	d.mu.Unlock()
	if empty {
		os.Exit(0)
	}
}

var _ io.Writer = ptyInput{}
