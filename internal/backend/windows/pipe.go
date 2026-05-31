//go:build windows

package conpty

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

// pipeConn is an io.ReadWriteCloser over a synchronous named-pipe handle. Reads
// and writes go straight to ReadFile/WriteFile so a relay goroutine can read on
// one side while another writes, and a closed peer surfaces as io.EOF rather
// than a raw Windows error.
type pipeConn struct {
	h         windows.Handle
	closeOnce sync.Once
}

func (c *pipeConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	var done uint32
	err := windows.ReadFile(c.h, p, &done, nil)
	if err != nil {
		if err == windows.ERROR_BROKEN_PIPE ||
			err == windows.ERROR_PIPE_NOT_CONNECTED {
			return int(done), io.EOF
		}
		return int(done), err
	}
	if done == 0 {
		return 0, io.EOF
	}
	return int(done), nil
}

func (c *pipeConn) Write(p []byte) (int, error) {
	var done uint32
	if err := windows.WriteFile(c.h, p, &done, nil); err != nil {
		return int(done), err
	}
	return int(done), nil
}

func (c *pipeConn) Close() error {
	c.closeOnce.Do(func() { windows.CloseHandle(c.h) })
	return nil
}

// pipeName is the per-user control pipe. Scoping by user keeps separate logins
// from sharing a daemon and matches tmux's per-user server socket.
func pipeName() string {
	user := sanitizeUser(os.Getenv("USERNAME"))
	if user == "" {
		user = "default"
	}
	return `\\.\pipe\wasa-` + user
}

func sanitizeUser(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// listenPipe creates a fresh instance of the control pipe. The first instance
// asserts FILE_FLAG_FIRST_PIPE_INSTANCE so a second daemon racing to start fails
// here rather than silently stealing connections.
func listenPipe(first bool) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(pipeName())
	if err != nil {
		return windows.InvalidHandle, err
	}
	openMode := uint32(windows.PIPE_ACCESS_DUPLEX)
	if first {
		openMode |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	h, err := windows.CreateNamedPipe(
		name,
		openMode,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT,
		windows.PIPE_UNLIMITED_INSTANCES,
		64*1024,
		64*1024,
		0,
		nil,
	)
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf(
			"conpty: create named pipe: %w",
			err,
		)
	}
	return h, nil
}

// acceptPipe blocks until a client connects to the pipe instance h. A client
// that connected in the race between CreateNamedPipe and ConnectNamedPipe shows
// up as ERROR_PIPE_CONNECTED and is a successful accept.
func acceptPipe(h windows.Handle) error {
	err := windows.ConnectNamedPipe(h, nil)
	if err == nil || err == windows.ERROR_PIPE_CONNECTED {
		return nil
	}
	return err
}

// dialPipe connects to the daemon's control pipe. It returns a nil conn and nil
// error when no daemon is listening (the pipe does not exist), so callers can
// treat "no daemon" like tmux treats "no server": an empty result, not a
// failure. A busy pipe is retried briefly while instances free up.
func dialPipe() (*pipeConn, error) {
	name, err := windows.UTF16PtrFromString(pipeName())
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		h, err := windows.CreateFile(
			name,
			windows.GENERIC_READ|windows.GENERIC_WRITE,
			0,
			nil,
			windows.OPEN_EXISTING,
			0,
			0,
		)
		if err == nil {
			return &pipeConn{h: h}, nil
		}
		if err == windows.ERROR_FILE_NOT_FOUND {
			return nil, nil
		}
		if err == windows.ERROR_PIPE_BUSY && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		return nil, fmt.Errorf("conpty: dial daemon: %w", err)
	}
}
