package link

import (
	"context"
	"math/rand/v2"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/joakimcarlsson/wasa-api/pkg/protocol"

	"github.com/joakimcarlsson/wasa-cli/internal/backend"
)

const (
	initialBackoff = time.Second
	maxBackoff     = time.Minute
)

// Loop keeps a websocket to the control plane alive: connect, hello, the
// latest registry snapshot, heartbeat on a ticker, reconnect on drop with
// capped exponential backoff and jitter, forever. states delivers fresh
// snapshots whenever the registry changes; while offline they simply
// overwrite each other and the next connect sends the latest — the runner
// never blocks on the control plane. host acts on the control plane's
// inbound requests: dispatches, session output subscriptions and input.
// The loop is completely silent: the cockpit owns the terminal and an
// offline api must not degrade local usage. It returns when ctx is
// cancelled or the token is rejected (revoked server-side).
func Loop(
	ctx context.Context,
	creds Credentials,
	version string,
	states <-chan protocol.State,
	host Host,
) {
	l := &runnerLink{
		creds:   creds,
		version: version,
		states:  states,
		host:    host,
	}
	backoff := initialBackoff
	for {
		connected, fatal := l.connectOnce(ctx)
		if fatal || ctx.Err() != nil {
			return
		}
		if connected {
			backoff = initialBackoff
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(jitter(backoff)):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

type runnerLink struct {
	creds   Credentials
	version string
	states  <-chan protocol.State
	host    Host
	latest  protocol.State
	have    bool
}

// pull drains any queued snapshots so latest holds the freshest one.
func (l *runnerLink) pull() {
	for {
		select {
		case st := <-l.states:
			l.latest, l.have = st, true
		default:
			return
		}
	}
}

// connectOnce dials, introduces the runner, reports its registry, and
// heartbeats until the connection drops. connected reports whether the
// session got as far as hello, which resets the caller's backoff; fatal
// means the api rejected the token and reconnecting is pointless.
func (l *runnerLink) connectOnce(
	ctx context.Context,
) (connected, fatal bool) {
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	conn, resp, err := websocket.Dial(
		dialCtx,
		strings.TrimSuffix(l.creds.URL, "/")+"/ws/runner",
		&websocket.DialOptions{
			HTTPHeader: http.Header{
				"Authorization": {"Bearer " + l.creds.Token},
			},
		},
	)
	cancel()
	if err != nil {
		if resp == nil {
			return false, false
		}
		if resp.Body != nil {
			resp.Body.Close()
		}
		return false, resp.StatusCode == http.StatusUnauthorized
	}
	defer conn.CloseNow()

	hello, err := protocol.EncodeHello(protocol.Hello{
		Name:    Hostname(),
		Version: l.version,
	})
	if err != nil {
		return false, false
	}
	if err := write(ctx, conn, hello); err != nil {
		return false, false
	}

	l.pull()
	if l.have {
		if err := l.sendState(ctx, conn); err != nil {
			return true, false
		}
	}

	in := newInbound(l.host, backend.Default())
	defer in.stop()

	readErr := make(chan error, 1)
	go func() {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				readErr <- err
				return
			}
			in.handle(ctx, data)
		}
	}()

	heartbeat, err := protocol.EncodeHeartbeat()
	if err != nil {
		return true, false
	}
	ticker := time.NewTicker(protocol.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			conn.Close(websocket.StatusNormalClosure, "shutting down")
			return true, true
		case <-readErr:
			return true, false
		case st := <-l.states:
			l.latest, l.have = st, true
			l.pull()
			if err := l.sendState(ctx, conn); err != nil {
				return true, false
			}
		case frame := <-in.out:
			if err := write(ctx, conn, frame); err != nil {
				return true, false
			}
		case <-ticker.C:
			if err := write(ctx, conn, heartbeat); err != nil {
				return true, false
			}
		}
	}
}

func (l *runnerLink) sendState(
	ctx context.Context,
	conn *websocket.Conn,
) error {
	data, err := protocol.EncodeState(l.latest)
	if err != nil {
		return err
	}
	return write(ctx, conn, data)
}

func write(ctx context.Context, conn *websocket.Conn, data []byte) error {
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, data)
}

// jitter spreads reconnects over ±25% of the backoff so a fleet of runners
// does not stampede a restarted api.
func jitter(d time.Duration) time.Duration {
	spread := d / 2
	return d - spread/2 + rand.N(spread)
}

// Hostname is the default runner name at login and the name sent in hello.
func Hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "runner"
	}
	return h
}
