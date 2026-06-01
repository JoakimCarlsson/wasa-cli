package sessionstatus

import (
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// WorkingWindow is how long after the last observed pane change a session still
// reads as working. A session producing output keeps changing its pane and so
// stays working; once it falls quiet for this long it settles into waiting or
// idle.
const WorkingWindow = 2 * time.Second

// spinnerRunes are the glyphs agent CLIs animate while busy. A quiescent pane
// whose tail still holds one is treated as idle rather than waiting: the
// heuristic refuses to read a frozen spinner as a prompt awaiting input. Only
// unambiguous glyphs are listed — the ASCII frames |/-\ are too common in paths
// and prose to use as a busy signal.
const spinnerRunes = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏⣾⣽⣻⢿⡿⣟⣯⣷◐◓◑◒"

// promptSuffixes are trailing characters that mark a line as a prompt awaiting
// input: shell prompts ($, #, %, the zsh/powerlevel ❯), a question or a colon
// from an interactive confirm. The match is on the last non-empty visible line.
const promptSuffixes = "$#%>❯:?"

// Heuristic derives an activity status from pane content alone — the only signal
// available for a program with no hook channel. sinceChange is how long the pane
// has been unchanged; visible is the pane text with escape sequences stripped. A
// pane that changed within window reads as Working; a quiescent pane whose tail
// looks like a prompt (and shows no spinner) reads as Waiting; anything else
// quiescent is Idle. It is intentionally conservative: when unsure it returns
// Idle rather than Waiting, so a wrong guess stays silent instead of crying for
// attention.
func Heuristic(visible string, sinceChange, window time.Duration) Status {
	if sinceChange < window {
		return Working
	}
	tail := lastMeaningfulLine(visible)
	if tail == "" {
		return Idle
	}
	if strings.ContainsAny(tail, spinnerRunes) {
		return Idle
	}
	if looksLikePrompt(tail) {
		return Waiting
	}
	return Idle
}

// lastMeaningfulLine returns the last non-blank line of visible, trimmed of
// trailing whitespace, skipping the empty rows common after a command's output
// so the heuristic judges the prompt line rather than blank space below it.
func lastMeaningfulLine(visible string) string {
	lines := strings.Split(visible, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if ln := strings.TrimRight(lines[i], " \t"); ln != "" {
			return ln
		}
	}
	return ""
}

// looksLikePrompt reports whether a visible line reads as input awaiting the
// user: it ends in a known prompt character, or it asks a yes/no question. The
// trailing rune is checked rune-aware so a multibyte glyph like ❯ matches.
func looksLikePrompt(line string) bool {
	r := []rune(line)
	last := r[len(r)-1]
	if strings.ContainsRune(promptSuffixes, last) {
		return true
	}
	lower := strings.ToLower(line)
	for _, marker := range []string{"(y/n)", "[y/n]", "yes/no", "press enter"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// paneObservation is the Tracker's per-session memory: the last visible pane
// text, when it last changed, and the status last derived for it.
type paneObservation struct {
	visible   string
	changedAt time.Time
	status    Status
}

// Tracker derives and remembers a heuristic status per session from a stream of
// pane snapshots. It lives entirely in memory and touches no persisted state.
// now is injectable so tests can drive the working window deterministically.
type Tracker struct {
	now    func() time.Time
	window time.Duration
	obs    map[string]*paneObservation
}

// NewTracker builds a Tracker using now as its clock.
func NewTracker(now func() time.Time) *Tracker {
	return &Tracker{
		now:    now,
		window: WorkingWindow,
		obs:    make(map[string]*paneObservation),
	}
}

// Observe feeds a fresh pane capture for session id and returns the status
// derived from it. raw may carry escape sequences; only the visible text drives
// change detection, so a colour-only repaint does not reset the working window.
func (t *Tracker) Observe(id, raw string) Status {
	visible := strings.TrimRight(ansi.Strip(raw), " \t\n")
	at := t.now()

	o := t.obs[id]
	if o == nil {
		t.obs[id] = &paneObservation{
			visible:   visible,
			changedAt: at,
			status:    Working,
		}
		return Working
	}

	if visible != o.visible {
		o.visible = visible
		o.changedAt = at
	}
	o.status = Heuristic(visible, at.Sub(o.changedAt), t.window)
	return o.status
}

// Forget drops every tracked session whose id is not in keep, so the tracker
// does not accumulate state for sessions removed from the registry.
func (t *Tracker) Forget(keep map[string]bool) {
	for id := range t.obs {
		if !keep[id] {
			delete(t.obs, id)
		}
	}
}
