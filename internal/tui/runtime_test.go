package tui

import (
	"testing"
	"time"
)

// fakeClock is a controllable time source for the status tracker, so the
// working window can be crossed deterministically without sleeping.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)}
}

func TestDeriveStatus(t *testing.T) {
	cases := []struct {
		name        string
		visible     string
		sinceChange time.Duration
		want        runtimeStatus
	}{
		{
			"recent change reads working",
			"anything",
			200 * time.Millisecond,
			statusWorking,
		},
		{
			"quiescent shell prompt waits",
			"user@host:~/repo$ ",
			5 * time.Second,
			statusWaiting,
		},
		{
			"quiescent zsh prompt waits",
			"~/repo ❯",
			5 * time.Second,
			statusWaiting,
		},
		{
			"quiescent confirm waits",
			"Proceed? (y/n)",
			5 * time.Second,
			statusWaiting,
		},
		{
			"quiescent spinner is idle",
			"⠋ Thinking…",
			5 * time.Second,
			statusIdle,
		},
		{
			"quiescent plain output is idle",
			"build succeeded",
			5 * time.Second,
			statusIdle,
		},
		{
			"quiescent empty pane is idle",
			"\n\n  \n",
			5 * time.Second,
			statusIdle,
		},
		{
			"prompt under blank lines still waits",
			"$ \n\n\n",
			5 * time.Second,
			statusWaiting,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := deriveStatus(c.visible, c.sinceChange, workingWindow)
			if got != c.want {
				t.Fatalf("deriveStatus(%q, %v) = %v, want %v",
					c.visible, c.sinceChange, got.label(), c.want.label())
			}
		})
	}
}

func TestStatusTrackerFirstObservationIsWorking(t *testing.T) {
	clk := newFakeClock()
	tr := newStatusTracker(clk.now)

	cur, prev := tr.observe("s1", "$ ")
	if cur != statusWorking {
		t.Fatalf("first observation = %v, want working", cur.label())
	}
	if prev != statusUnknown {
		t.Fatalf("first observation prev = %v, want unknown", prev.label())
	}
}

func TestStatusTrackerSettlesToWaiting(t *testing.T) {
	clk := newFakeClock()
	tr := newStatusTracker(clk.now)

	tr.observe("s1", "user@host$ ")
	clk.advance(workingWindow + time.Second)

	cur, prev := tr.observe("s1", "user@host$ ")
	if cur != statusWaiting {
		t.Fatalf("quiescent prompt = %v, want waiting", cur.label())
	}
	if prev != statusWorking {
		t.Fatalf("prev = %v, want working", prev.label())
	}
}

func TestStatusTrackerChangeResetsToWorking(t *testing.T) {
	clk := newFakeClock()
	tr := newStatusTracker(clk.now)

	tr.observe("s1", "$ ")
	clk.advance(workingWindow + time.Second)
	tr.observe("s1", "$ ") // now waiting

	clk.advance(time.Second)
	cur, prev := tr.observe("s1", "running command output")
	if cur != statusWorking {
		t.Fatalf("changed pane = %v, want working", cur.label())
	}
	if prev != statusWaiting {
		t.Fatalf("prev = %v, want waiting", prev.label())
	}
}

func TestStatusTrackerIgnoresEscapeOnlyChange(t *testing.T) {
	clk := newFakeClock()
	tr := newStatusTracker(clk.now)

	tr.observe("s1", "\x1b[32m$ \x1b[0m")
	clk.advance(workingWindow + time.Second)

	// Same visible text, different escape bytes: must stay quiescent, not
	// flip back to working on a colour-only repaint.
	cur, _ := tr.observe("s1", "\x1b[1m$ \x1b[0m")
	if cur != statusWaiting {
		t.Fatalf("escape-only change = %v, want waiting", cur.label())
	}
}

func TestStatusTrackerMarkExited(t *testing.T) {
	clk := newFakeClock()
	tr := newStatusTracker(clk.now)

	tr.observe("s1", "output")
	prev := tr.markExited("s1")
	if prev != statusWorking {
		t.Fatalf("markExited prev = %v, want working", prev.label())
	}
	if got := tr.status("s1"); got != statusExited {
		t.Fatalf("status after markExited = %v, want exited", got.label())
	}
}

func TestStatusTrackerForget(t *testing.T) {
	clk := newFakeClock()
	tr := newStatusTracker(clk.now)

	tr.observe("s1", "a")
	tr.observe("s2", "b")
	tr.forget(map[string]bool{"s1": true})

	if got := tr.status("s1"); got == statusUnknown {
		t.Fatal("forget dropped a kept session")
	}
	if got := tr.status("s2"); got != statusUnknown {
		t.Fatal("forget kept a dropped session")
	}
}
