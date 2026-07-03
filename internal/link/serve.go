package link

import (
	"context"
	"sync"

	"github.com/joakimcarlsson/wasa-api/pkg/protocol"

	"github.com/joakimcarlsson/wasa-cli/internal/backend"
)

// Host is the cockpit surface the link loop drives when the control plane
// asks for work. Implementations marshal calls onto the cockpit's update
// goroutine — the registry behind them is not safe for concurrent use.
type Host interface {
	// Dispatch creates a session for the given intent and answers with the
	// result; it must always answer, a failure as ok=false with a code.
	Dispatch(
		ctx context.Context,
		d protocol.Dispatch,
	) protocol.DispatchResult

	// SessionTarget resolves a session id to the backend session name input
	// and output streaming target, false for an unknown or exited session.
	SessionTarget(ctx context.Context, sessionID string) (string, bool)
}

// inbound handles control-plane frames for one connection: dispatches into
// the host, session output streams, and input injection. Outbound frames go
// through out, drained by the connection's write loop; done closes when the
// connection is torn down.
type inbound struct {
	host Host
	be   backend.SessionBackend
	out  chan []byte
	done chan struct{}

	mu      sync.Mutex
	stopped bool
	streams map[string]backend.Watcher
}

func newInbound(host Host, be backend.SessionBackend) *inbound {
	return &inbound{
		host: host,
		be:   be,
		out:  make(chan []byte, 64),
		done: make(chan struct{}),
		streams: make(
			map[string]backend.Watcher,
		),
	}
}

// handle processes one raw frame from the control plane. Unknown or
// malformed frames are dropped — the runner never dies on bad input.
func (in *inbound) handle(ctx context.Context, data []byte) {
	env, err := protocol.Decode(data)
	if err != nil {
		return
	}
	switch env.Type {
	case protocol.TypeDispatch:
		d, err := env.Dispatch()
		if err != nil {
			return
		}
		go in.dispatch(ctx, d)
	case protocol.TypeSubscribe:
		ref, err := env.SessionRef()
		if err != nil {
			return
		}
		go in.subscribe(ctx, ref.SessionID)
	case protocol.TypeUnsubscribe:
		ref, err := env.SessionRef()
		if err != nil {
			return
		}
		in.unsubscribe(ref.SessionID)
	case protocol.TypeSessionInput:
		msg, err := env.SessionInput()
		if err != nil {
			return
		}
		go in.input(ctx, msg)
	}
}

// stop tears down every stream and unblocks pending sends. Called once when
// the connection ends; subscriptions do not survive a reconnect — the
// control plane re-subscribes.
func (in *inbound) stop() {
	close(in.done)
	in.mu.Lock()
	in.stopped = true
	streams := in.streams
	in.streams = nil
	in.mu.Unlock()
	for _, w := range streams {
		_ = w.Close()
	}
}

// send queues an outbound frame, giving up when the connection is gone.
func (in *inbound) send(frame []byte) {
	select {
	case in.out <- frame:
	case <-in.done:
	}
}

func (in *inbound) dispatch(ctx context.Context, d protocol.Dispatch) {
	res := in.host.Dispatch(ctx, d)
	res.DispatchID = d.DispatchID
	frame, err := protocol.EncodeDispatchResult(res)
	if err != nil {
		return
	}
	in.send(frame)
}

// subscribe starts streaming the session's pane content as session_output
// frames. Each chunk is a full deduplicated pane capture (see
// backend.Watcher), so the consumer renders the latest chunk as the current
// view. An unknown session or a backend without streaming produces nothing.
func (in *inbound) subscribe(ctx context.Context, sessionID string) {
	in.mu.Lock()
	_, active := in.streams[sessionID]
	stopped := in.stopped
	in.mu.Unlock()
	if active || stopped {
		return
	}

	name, ok := in.host.SessionTarget(ctx, sessionID)
	if !ok {
		return
	}
	sb, ok := in.be.(backend.StreamingBackend)
	if !ok {
		return
	}
	w, err := sb.Watch(name)
	if err != nil {
		return
	}

	in.mu.Lock()
	if _, active := in.streams[sessionID]; active || in.stopped {
		in.mu.Unlock()
		_ = w.Close()
		return
	}
	in.streams[sessionID] = w
	in.mu.Unlock()

	go in.pump(sessionID, w)
}

// pump forwards a stream's captures until it ends — by unsubscribe, session
// exit or connection teardown — then clears the bookkeeping.
func (in *inbound) pump(sessionID string, w backend.Watcher) {
	var seq uint64
	for chunk := range w.Updates() {
		seq++
		frame, err := protocol.EncodeSessionOutput(protocol.SessionOutput{
			SessionID: sessionID,
			Seq:       seq,
			Chunk:     chunk,
		})
		if err != nil {
			continue
		}
		select {
		case in.out <- frame:
		case <-in.done:
			_ = w.Close()
		}
	}
	in.unsubscribe(sessionID)
}

func (in *inbound) unsubscribe(sessionID string) {
	in.mu.Lock()
	w := in.streams[sessionID]
	delete(in.streams, sessionID)
	in.mu.Unlock()
	if w != nil {
		_ = w.Close()
	}
}

// input types the payload into the session's pane, verbatim. Failures are
// silently dropped: the loop stays quiet and the terminal on the runner
// remains authoritative.
func (in *inbound) input(ctx context.Context, msg protocol.SessionInput) {
	name, ok := in.host.SessionTarget(ctx, msg.SessionID)
	if !ok {
		return
	}
	ib, ok := in.be.(backend.InputBackend)
	if !ok {
		return
	}
	_ = ib.SendKeys(name, msg.Data)
}
