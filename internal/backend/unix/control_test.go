//go:build !windows

package tmux

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// newTestConn builds a ControlConn with only its channels wired, so consume can
// be driven against an in-memory stream without a real tmux process.
func newTestConn() *ControlConn {
	return &ControlConn{
		updates: make(chan string, 1),
		trigger: make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
}

// drain returns the values buffered on updates after consume has returned. The
// channel holds at most one (the latest) because deliver replaces stale values.
func drain(cc *ControlConn) []string {
	var out []string
	for {
		select {
		case v := <-cc.updates:
			out = append(out, v)
		default:
			return out
		}
	}
}

func TestConsumeDeliversBlockPreservingEscapes(t *testing.T) {
	cc := newTestConn()
	in := "%begin 1 7 1\n" +
		"\x1b[31mRED\x1b[39m\n" +
		"plain\n" +
		"%end 1 7 1\n"
	cc.consume(strings.NewReader(in))

	got := drain(cc)
	if len(got) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(got))
	}
	want := "\x1b[31mRED\x1b[39m\nplain"
	if got[0] != want {
		t.Fatalf("content = %q, want %q (escapes must survive)", got[0], want)
	}
}

func TestConsumeDropsIdenticalCaptures(t *testing.T) {
	cc := newTestConn()
	block := "%begin 1 7 1\nsame\n%end 1 7 1\n"
	cc.consume(strings.NewReader(block + block + block))

	if got := drain(cc); len(got) != 1 {
		t.Fatalf("identical captures delivered %d times, want 1", len(got))
	}
}

func TestConsumeKeepsLatestOfDistinctCaptures(t *testing.T) {
	cc := newTestConn()
	in := "%begin 1 7 1\nfirst\n%end 1 7 1\n" +
		"%begin 1 8 1\nsecond\n%end 1 8 1\n"
	cc.consume(strings.NewReader(in))

	got := drain(cc)
	if len(got) != 1 || got[0] != "second" {
		t.Fatalf("buffered = %v, want only the latest [second]", got)
	}
}

func TestConsumeOutputSignalsCapture(t *testing.T) {
	cc := newTestConn()
	cc.consume(strings.NewReader("%output %1 some pane bytes\\015\\012\n"))

	select {
	case <-cc.trigger:
	default:
		t.Fatal("output notification did not signal a capture")
	}
}

func TestConsumeDiscardsErrorBlock(t *testing.T) {
	cc := newTestConn()
	in := "%begin 1 7 1\njunk from a failed command\n%error 1 7 1\n"
	cc.consume(strings.NewReader(in))

	if got := drain(cc); len(got) != 0 {
		t.Fatalf("error block delivered %v, want nothing", got)
	}
}

func TestConsumeStopsOnExit(t *testing.T) {
	cc := newTestConn()
	in := "%exit\n%begin 1 7 1\nafter exit\n%end 1 7 1\n"
	cc.consume(strings.NewReader(in))

	if got := drain(cc); len(got) != 0 {
		t.Fatalf("read past %%exit: delivered %v", got)
	}
}

// TestWatchStreamsLiveOutput drives a real tmux server end to end: it spawns a
// session emitting a known colored line, watches it over control mode, and
// asserts the line arrives with its escape sequences intact, that closing the
// watch leaves the session alive (closing the control client must not kill it),
// and that the streamed delivery happened far faster than the old 750ms poll.
func TestWatchStreamsLiveOutput(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH; skipping live control-mode test")
	}
	c := New()
	name := "wasa_ctl_streamtest"
	_ = c.Kill(name)
	prog := []string{
		"sh", "-c", "printf '\\033[31mLIVEMARK\\033[39m\\n'; sleep 30",
	}
	if err := c.Spawn(name, "", prog...); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { _ = c.Kill(name) })

	start := time.Now()
	w, err := c.Watch(name)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	var got string
	deadline := time.After(3 * time.Second)
	for !strings.Contains(got, "LIVEMARK") {
		select {
		case content, ok := <-w.Updates():
			if !ok {
				t.Fatal("stream closed before the marker arrived")
			}
			got = content
		case <-deadline:
			t.Fatalf("marker not streamed within 3s; last capture %q", got)
		}
	}
	if elapsed := time.Since(start); elapsed > 700*time.Millisecond {
		t.Fatalf(
			"first capture took %v, slower than the old 750ms poll",
			elapsed,
		)
	}
	if !strings.Contains(got, "\x1b[31m") {
		t.Fatalf("streamed capture dropped the color escape: %q", got)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if has, err := c.Has(name); err != nil || !has {
		t.Fatalf(
			"session gone after closing the watch (has=%v err=%v)",
			has,
			err,
		)
	}
}
