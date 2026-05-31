//go:build windows

package conpty

import (
	"io"
	"sync"
)

// ringBytes is how much recent pseudo-console output each session retains. On
// attach the daemon replays this so the terminal repaints to roughly the live
// state instead of opening blank; it comfortably exceeds one screen.
const ringBytes = 256 << 10

// session is one live pseudo-console owned by the daemon: the conPty itself, a
// screen emulator kept current for Capture, a bounded ring of raw output for
// attach replay, and the writer of the currently-attached client (nil when
// detached). A single reader goroutine drains the pseudo-console and fans output
// out to all three.
//
// Two locks guard distinct concerns. mu protects the screen, ring and attached
// fields. wmu serialises the actual writes to the attached client so the
// scrollback replay always lands ahead of live output and the reader goroutine
// never races the replay on the same pipe handle.
type session struct {
	name    string
	dir     string
	program []string

	cpty *conPty

	mu       sync.Mutex
	scr      *screen
	ring     []byte
	cols     int16
	rows     int16
	attached io.Writer

	wmu sync.Mutex

	onExit func(name string)
	done   chan struct{}
}

func newSession(
	name, dir string,
	program []string,
	cols, rows int16,
	cpty *conPty,
	onExit func(string),
) *session {
	s := &session{
		name:    name,
		dir:     dir,
		program: program,
		cpty:    cpty,
		scr:     newScreen(int(cols), int(rows)),
		cols:    cols,
		rows:    rows,
		onExit:  onExit,
		done:    make(chan struct{}),
	}
	go s.readLoop()
	return s
}

// readLoop drains the pseudo-console until it closes, updating the screen and
// ring under mu and forwarding live output to the attached client under wmu.
// When the child exits the loop reports it through onExit so the daemon can drop
// the session, matching tmux ending a session when its program dies.
func (s *session) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := s.cpty.out.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			s.mu.Lock()
			s.scr.Write(chunk)
			s.appendRing(chunk)
			w := s.attached
			s.mu.Unlock()
			if w != nil {
				s.wmu.Lock()
				_, _ = w.Write(chunk)
				s.wmu.Unlock()
			}
		}
		if err != nil {
			break
		}
	}
	close(s.done)
	s.cpty.Close()
	if s.onExit != nil {
		s.onExit(s.name)
	}
}

func (s *session) appendRing(chunk []byte) {
	s.ring = append(s.ring, chunk...)
	if len(s.ring) > ringBytes {
		s.ring = append([]byte(nil), s.ring[len(s.ring)-ringBytes:]...)
	}
}

// Capture returns the current screen as plain text.
func (s *session) Capture() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scr.Snapshot()
}

// Resize matches the pseudo-console and screen to a new terminal size.
func (s *session) Resize(cols, rows int16) {
	if cols <= 0 || rows <= 0 {
		return
	}
	s.mu.Lock()
	s.cols, s.rows = cols, rows
	s.scr.Resize(int(cols), int(rows))
	s.mu.Unlock()
	_ = s.cpty.Resize(cols, rows)
}

// stream wires an attached client to the session: it registers conn as the live
// output sink, replays the scrollback ring in order ahead of any live output,
// then blocks copying the client's input into the pseudo-console until the
// client disconnects (detaches). The session stays alive after stream returns.
func (s *session) stream(conn io.ReadWriter) {
	s.wmu.Lock()
	s.mu.Lock()
	s.attached = conn
	replay := append([]byte(nil), s.ring...)
	s.mu.Unlock()
	_, _ = conn.Write(replay)
	s.wmu.Unlock()

	_, _ = io.Copy(ptyInput{s}, conn)

	s.mu.Lock()
	if s.attached == conn {
		s.attached = nil
	}
	s.mu.Unlock()
}

// ptyInput adapts a session to an io.Writer that feeds the pseudo-console's
// input, so client input can be streamed in with io.Copy.
type ptyInput struct{ s *session }

func (p ptyInput) Write(b []byte) (int, error) {
	return p.s.cpty.in.Write(b)
}
