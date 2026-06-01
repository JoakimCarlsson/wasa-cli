package sessionstatus

import (
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)}
}

func TestHeuristic(t *testing.T) {
	cases := []struct {
		name        string
		visible     string
		sinceChange time.Duration
		want        Status
	}{
		{
			"recent change is working",
			"anything",
			200 * time.Millisecond,
			Working,
		},
		{
			"quiescent shell prompt waits",
			"user@host:~/repo$ ",
			5 * time.Second,
			Waiting,
		},
		{"quiescent zsh prompt waits", "~/repo ❯", 5 * time.Second, Waiting},
		{"quiescent confirm waits", "Proceed? (y/n)", 5 * time.Second, Waiting},
		{"quiescent spinner is idle", "⠋ Thinking…", 5 * time.Second, Idle},
		{
			"quiescent plain output is idle",
			"build succeeded",
			5 * time.Second,
			Idle,
		},
		{"quiescent empty pane is idle", "\n\n  \n", 5 * time.Second, Idle},
		{
			"prompt under blank lines waits",
			"$ \n\n\n",
			5 * time.Second,
			Waiting,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Heuristic(
				c.visible,
				c.sinceChange,
				WorkingWindow,
			); got != c.want {
				t.Fatalf("Heuristic(%q, %v) = %q, want %q",
					c.visible, c.sinceChange, got, c.want)
			}
		})
	}
}

func TestTrackerFirstObservationIsWorking(t *testing.T) {
	tr := NewTracker(newFakeClock().now)
	if got := tr.Observe("s1", "$ "); got != Working {
		t.Fatalf("first observation = %q, want working", got)
	}
}

func TestTrackerSettlesWhenQuiet(t *testing.T) {
	clk := newFakeClock()
	tr := NewTracker(clk.now)

	tr.Observe("s1", "user@host$ ")
	clk.advance(WorkingWindow + time.Second)
	if got := tr.Observe("s1", "user@host$ "); got != Waiting {
		t.Fatalf("quiescent prompt = %q, want waiting", got)
	}
}

func TestTrackerChangeResetsToWorking(t *testing.T) {
	clk := newFakeClock()
	tr := NewTracker(clk.now)

	tr.Observe("s1", "$ ")
	clk.advance(WorkingWindow + time.Second)
	tr.Observe("s1", "$ ")
	clk.advance(time.Second)
	if got := tr.Observe("s1", "running output"); got != Working {
		t.Fatalf("changed pane = %q, want working", got)
	}
}

func TestTrackerIgnoresEscapeOnlyChange(t *testing.T) {
	clk := newFakeClock()
	tr := NewTracker(clk.now)

	tr.Observe("s1", "\x1b[32m$ \x1b[0m")
	clk.advance(WorkingWindow + time.Second)
	if got := tr.Observe("s1", "\x1b[1m$ \x1b[0m"); got != Waiting {
		t.Fatalf("escape-only change = %q, want waiting", got)
	}
}

func TestTrackerForget(t *testing.T) {
	tr := NewTracker(newFakeClock().now)
	tr.Observe("s1", "a")
	tr.Observe("s2", "b")
	tr.Forget(map[string]bool{"s1": true})

	if _, ok := tr.obs["s1"]; !ok {
		t.Fatal("forget dropped a kept session")
	}
	if _, ok := tr.obs["s2"]; ok {
		t.Fatal("forget kept a dropped session")
	}
}
