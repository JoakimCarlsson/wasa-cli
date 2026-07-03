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
)

const (
	initialBackoff = time.Second
	maxBackoff     = time.Minute
)

// Loop keeps a websocket to the control plane alive: connect, hello,
// heartbeat on a ticker, reconnect on drop with capped exponential backoff and
// jitter, forever. It is completely silent — the cockpit owns the terminal
// and an offline api must not degrade local usage. It returns when ctx is
// cancelled or the token is rejected (revoked server-side).
func Loop(ctx context.Context, creds Credentials, version string) {
	backoff := initialBackoff
	for {
		connected, fatal := connectOnce(ctx, creds, version)
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

// connectOnce dials, introduces the runner, and heartbeats until the
// connection drops. connected reports whether the session got as far as
// hello, which resets the caller's backoff; fatal means the api rejected the
// token and reconnecting is pointless.
func connectOnce(
	ctx context.Context,
	creds Credentials,
	version string,
) (connected, fatal bool) {
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	conn, resp, err := websocket.Dial(
		dialCtx,
		strings.TrimSuffix(creds.URL, "/")+"/ws/runner",
		&websocket.DialOptions{
			HTTPHeader: http.Header{
				"Authorization": {"Bearer " + creds.Token},
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
		Version: version,
	})
	if err != nil {
		return false, false
	}
	if err := write(ctx, conn, hello); err != nil {
		return false, false
	}

	readErr := make(chan error, 1)
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				readErr <- err
				return
			}
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
		case <-ticker.C:
			if err := write(ctx, conn, heartbeat); err != nil {
				return true, false
			}
		}
	}
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
