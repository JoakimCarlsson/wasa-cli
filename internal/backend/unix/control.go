//go:build !windows

package tmux

// This file adds control-mode streaming for the cockpit preview. Where Capture
// spawns a fresh `tmux capture-pane` subprocess per call, ControlConn holds one
// long-lived `tmux -C attach-session` control client: a reader goroutine watches
// the control protocol for %output (pane activity) notifications and a writer
// goroutine issues capture-pane commands over the same connection, debounced so
// a burst of output coalesces into one capture and a quiet pane still refreshes
// on a low-frequency fallback. Identical captures are dropped at the source so
// an idle session produces no churn.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	// captureDebounce coalesces a burst of pane output into at most one capture
	// per window, so heavy continuous output does not issue a capture per line.
	captureDebounce = 75 * time.Millisecond

	// captureFallback forces a capture even with no %output notification, to
	// cover a missed event or a momentarily quiet connection. It replaces the
	// old blind 750ms hot poll with a much slower safety net.
	captureFallback = 2 * time.Second
)

// ControlConn is one tmux control-mode client streaming a single session's pane
// content. It satisfies backend.Watcher. Create it with Client.Watch and Close
// it when the session stops being previewed; Close leaves no orphaned control
// client and never kills the underlying session.
type ControlConn struct {
	name  string
	cmd   *exec.Cmd
	stdin io.WriteCloser

	updates chan string
	trigger chan struct{}
	done    chan struct{}

	closeOnce sync.Once
}

// Watch opens a control-mode connection to the named session and returns a
// Watcher that streams its pane content. It captures once on connect so the
// preview populates immediately. $TMUX is cleared on the control client's
// environment exactly as AttachCmd does, so the stream attaches even when wasa
// itself runs inside tmux. A failure to start tmux is returned so the caller can
// fall back to the one-shot Capture poll.
func (c *Client) Watch(name string) (*ControlConn, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}

	cmd := exec.Command(c.bin(), "-C", "attach-session", "-t", name)
	cmd.Env = envWithout(os.Environ(), "TMUX")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("tmux -C: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("tmux -C: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, notInstalled(err)
		}
		return nil, fmt.Errorf("tmux -C attach-session: %w", err)
	}

	cc := &ControlConn{
		name:    name,
		cmd:     cmd,
		stdin:   stdin,
		updates: make(chan string, 1),
		trigger: make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	go cc.read(stdout)
	go cc.pump()
	cc.signal()
	return cc, nil
}

// Updates returns the channel of fresh pane captures. It is closed when the
// connection ends.
func (cc *ControlConn) Updates() <-chan string { return cc.updates }

// Close stops the control client: it signals the goroutines to stop, closes
// stdin so tmux -C detaches, and kills the process as a backstop so no orphaned
// control client survives. Killing the control client detaches it; the
// underlying session keeps running. The reader goroutine reaps the process and
// closes Updates once stdout drains. Safe to call more than once.
func (cc *ControlConn) Close() error {
	cc.closeOnce.Do(func() {
		close(cc.done)
		_ = cc.stdin.Close()
		if cc.cmd.Process != nil {
			_ = cc.cmd.Process.Kill()
		}
	})
	return nil
}

// read consumes the control protocol on stdout. %output notifications become
// capture triggers; %begin/%end blocks are the capture-pane replies, joined
// into pane content and delivered unless byte-identical to the last one. It runs
// until %exit or EOF (the connection ended or Close killed the process), then
// reaps the process and closes Updates so a blocked consumer observes the end.
func (cc *ControlConn) read(stdout io.Reader) {
	defer func() {
		cc.Close()
		_ = cc.cmd.Wait()
		close(cc.updates)
	}()
	cc.consume(stdout)
}

// consume parses the control protocol from r, signalling captures on %output and
// delivering deduplicated capture blocks, until %exit or EOF. It is the pure
// protocol loop, separated from read's process lifecycle so it can be tested
// against an in-memory stream.
func (cc *ControlConn) consume(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var (
		inBlock  bool
		block    []string
		last     string
		haveLast bool
	)
	for sc.Scan() {
		line := strings.TrimSuffix(sc.Text(), "\r")
		switch {
		case inBlock:
			switch {
			case strings.HasPrefix(line, "%end"):
				inBlock = false
				content := strings.Join(block, "\n")
				block = block[:0]
				if haveLast && content == last {
					continue
				}
				last, haveLast = content, true
				cc.deliver(content)
			case strings.HasPrefix(line, "%error"):
				inBlock = false
				block = block[:0]
			default:
				block = append(block, line)
			}
		case strings.HasPrefix(line, "%begin"):
			inBlock = true
			block = block[:0]
		case strings.HasPrefix(line, "%output"):
			cc.signal()
		case strings.HasPrefix(line, "%exit"):
			return
		}
	}
}

// pump owns stdin. It issues a capture on a debounced pane-activity trigger and
// on the fallback ticker, so a burst of output coalesces into one capture and a
// quiet connection still refreshes.
func (cc *ControlConn) pump() {
	fallback := time.NewTicker(captureFallback)
	defer fallback.Stop()

	var debounce <-chan time.Time
	for {
		select {
		case <-cc.done:
			return
		case <-cc.trigger:
			if debounce == nil {
				debounce = time.After(captureDebounce)
			}
		case <-debounce:
			debounce = nil
			cc.capture()
		case <-fallback.C:
			cc.capture()
		}
	}
}

// signal requests a capture without blocking the reader; the buffered trigger
// coalesces requests so a flood of %output enqueues at most one pending capture.
func (cc *ControlConn) signal() {
	select {
	case cc.trigger <- struct{}{}:
	default:
	}
}

// capture issues a capture-pane over the persistent connection. -e preserves the
// pane's escape sequences (colors), -J joins soft-wrapped lines back into one
// logical line, and -p writes the block to the reply stream, matching the
// one-shot Capture path.
func (cc *ControlConn) capture() {
	_, _ = io.WriteString(cc.stdin, "capture-pane -p -e -J -t "+cc.name+"\n")
}

// deliver hands content to the consumer, replacing any value it has not yet
// taken so the consumer always sees the newest capture and the reader never
// stalls behind a slow Update loop.
func (cc *ControlConn) deliver(content string) {
	select {
	case <-cc.updates:
	default:
	}
	select {
	case cc.updates <- content:
	case <-cc.done:
	}
}
