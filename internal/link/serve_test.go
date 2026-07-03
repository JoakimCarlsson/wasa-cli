package link

import (
	"context"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/joakimcarlsson/wasa-api/pkg/protocol"

	"github.com/joakimcarlsson/wasa-cli/internal/backend"
)

type fakeHost struct {
	target string
}

func (h *fakeHost) Dispatch(
	_ context.Context,
	d protocol.Dispatch,
) protocol.DispatchResult {
	return protocol.DispatchResult{OK: true, SessionID: "s-" + d.Intent}
}

func (h *fakeHost) SessionTarget(
	_ context.Context,
	_ string,
) (string, bool) {
	if h.target == "" {
		return "", false
	}
	return h.target, true
}

type fakeWatcher struct {
	updates chan string
	once    sync.Once
}

func (w *fakeWatcher) Updates() <-chan string { return w.updates }

func (w *fakeWatcher) Close() error {
	w.once.Do(func() { close(w.updates) })
	return nil
}

type fakeBackend struct {
	mu      sync.Mutex
	watcher *fakeWatcher
	keys    []string
}

func (b *fakeBackend) SpawnEnv(string, string, []string, ...string) error {
	return nil
}
func (b *fakeBackend) AttachCmd(string) (*exec.Cmd, error) { return nil, nil }
func (b *fakeBackend) Capture(string) (string, error)      { return "", nil }
func (b *fakeBackend) Has(string) (bool, error)            { return true, nil }
func (b *fakeBackend) List() ([]string, error)             { return nil, nil }
func (b *fakeBackend) Kill(string) error                   { return nil }

func (b *fakeBackend) Watch(string) (backend.Watcher, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.watcher = &fakeWatcher{updates: make(chan string, 4)}
	return b.watcher, nil
}

func (b *fakeBackend) SendKeys(name, data string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.keys = append(b.keys, name+":"+data)
	return nil
}

func frame(t *testing.T, msgType string, payload any) []byte {
	t.Helper()
	data, err := protocol.Encode(msgType, payload)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return data
}

func nextFrame(t *testing.T, in *inbound) protocol.Envelope {
	t.Helper()
	select {
	case data := <-in.out:
		env, err := protocol.Decode(data)
		if err != nil {
			t.Fatalf("decode outbound: %v", err)
		}
		return env
	case <-time.After(2 * time.Second):
		t.Fatal("no outbound frame")
		return protocol.Envelope{}
	}
}

func TestInboundDispatchReplies(t *testing.T) {
	in := newInbound(&fakeHost{}, &fakeBackend{})
	defer in.stop()

	in.handle(t.Context(), frame(t, protocol.TypeDispatch,
		protocol.Dispatch{DispatchID: "d1", WorkspaceID: "w1", Intent: "go"},
	))

	env := nextFrame(t, in)
	if env.Type != protocol.TypeDispatchResult {
		t.Fatalf("type = %q", env.Type)
	}
	res, err := env.DispatchResult()
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	if res.DispatchID != "d1" || !res.OK || res.SessionID != "s-go" {
		t.Fatalf("result = %+v", res)
	}
}

func TestInboundSubscribeStreamsAndStops(t *testing.T) {
	be := &fakeBackend{}
	in := newInbound(&fakeHost{target: "wasa_a_b"}, be)
	defer in.stop()

	sub := frame(t, protocol.TypeSubscribe,
		protocol.SessionRef{SessionID: "s1"},
	)
	in.handle(t.Context(), sub)

	deadline := time.Now().Add(2 * time.Second)
	for {
		be.mu.Lock()
		w := be.watcher
		be.mu.Unlock()
		if w != nil {
			w.updates <- "pane one"
			w.updates <- "pane two"
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("watcher never opened")
		}
		time.Sleep(5 * time.Millisecond)
	}

	first := nextFrame(t, in)
	out, err := first.SessionOutput()
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	if out.SessionID != "s1" || out.Seq != 1 || out.Chunk != "pane one" {
		t.Fatalf("chunk 1 = %+v", out)
	}
	out, err = nextFrame(t, in).SessionOutput()
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	if out.Seq != 2 || out.Chunk != "pane two" {
		t.Fatalf("chunk 2 = %+v", out)
	}

	in.handle(t.Context(), frame(t, protocol.TypeUnsubscribe,
		protocol.SessionRef{SessionID: "s1"},
	))
	select {
	case <-be.watcher.updates:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher not closed after unsubscribe")
	}
}

func TestInboundInputSendsKeys(t *testing.T) {
	be := &fakeBackend{}
	in := newInbound(&fakeHost{target: "wasa_a_b"}, be)
	defer in.stop()

	in.handle(t.Context(), frame(t, protocol.TypeSessionInput,
		protocol.SessionInput{SessionID: "s1", Data: "yes\r"},
	))

	deadline := time.Now().Add(2 * time.Second)
	for {
		be.mu.Lock()
		n := len(be.keys)
		var got string
		if n > 0 {
			got = be.keys[0]
		}
		be.mu.Unlock()
		if n > 0 {
			if got != "wasa_a_b:yes\r" {
				t.Fatalf("keys = %q", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("send-keys never called")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestInboundUnknownSessionIgnored(t *testing.T) {
	be := &fakeBackend{}
	in := newInbound(&fakeHost{}, be)
	defer in.stop()

	in.handle(t.Context(), frame(t, protocol.TypeSubscribe,
		protocol.SessionRef{SessionID: "nope"},
	))
	in.handle(t.Context(), frame(t, protocol.TypeSessionInput,
		protocol.SessionInput{SessionID: "nope", Data: "x"},
	))

	time.Sleep(20 * time.Millisecond)
	be.mu.Lock()
	defer be.mu.Unlock()
	if be.watcher != nil || len(be.keys) != 0 {
		t.Fatalf("acted on unknown session: %+v %v", be.watcher, be.keys)
	}
}
