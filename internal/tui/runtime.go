package tui

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/joakimcarlsson/wasa/internal/config"
)

// runtimeStatus is the cockpit's best-effort, per-session liveness state. It is
// derived from the session's live pane content and is never persisted: the
// registry's running/exited remains the source of truth for liveness, and a
// mis-derivation here can only mislabel a dot, never corrupt state.
type runtimeStatus int

const (
	// statusUnknown is a running session not yet observed; it renders like
	// working until the first pane snapshot arrives, and never triggers a
	// transition notification.
	statusUnknown runtimeStatus = iota
	statusWorking
	statusWaiting
	statusIdle
	statusExited
)

func (s runtimeStatus) label() string {
	switch s {
	case statusWorking:
		return "working"
	case statusWaiting:
		return "waiting"
	case statusIdle:
		return "idle"
	case statusExited:
		return "exited"
	default:
		return "running"
	}
}

const (
	// workingWindow is how long after the last observed pane change a session
	// still reads as working. A session producing output keeps changing its
	// pane and so stays working; once it falls quiet for this long it settles
	// into waiting or idle.
	workingWindow = 2 * time.Second

	// notifyDebounce is the minimum gap between two notifications for the same
	// session, so a session that flaps between states cannot produce a burst.
	notifyDebounce = 5 * time.Second
)

// spinnerRunes are the glyphs agent CLIs animate while busy. A quiescent pane
// whose tail still holds one of these is treated as idle rather than waiting:
// the heuristic refuses to read a frozen spinner as a prompt awaiting input.
// Only unambiguous spinner glyphs are listed — the ASCII frames |/-\ are too
// common in paths and prose to use as a busy signal.
const spinnerRunes = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏⣾⣽⣻⢿⡿⣟⣯⣷◐◓◑◒"

// promptSuffixes are trailing characters that mark a line as a prompt awaiting
// input: shell prompts ($, #, %, the zsh/powerlevel ❯), a question or a colon
// from an interactive confirm. The match is on the last non-empty visible line.
const promptSuffixes = "$#%>❯:?"

// deriveStatus is the heuristic at the heart of the live status. sinceChange is
// how long the pane content has been unchanged; visible is the current pane
// text with escape sequences already stripped. A pane that changed within the
// working window reads as working; a quiescent pane whose tail looks like a
// prompt (and shows no spinner) reads as waiting; anything else quiescent is
// idle. It is intentionally conservative: when unsure it returns idle rather
// than waiting, so a wrong guess stays silent instead of crying for attention.
func deriveStatus(
	visible string,
	sinceChange, window time.Duration,
) runtimeStatus {
	if sinceChange < window {
		return statusWorking
	}
	tail := lastMeaningfulLine(visible)
	if tail == "" {
		return statusIdle
	}
	if strings.ContainsAny(tail, spinnerRunes) {
		return statusIdle
	}
	if looksLikePrompt(tail) {
		return statusWaiting
	}
	return statusIdle
}

// lastMeaningfulLine returns the last non-blank line of visible, trimmed of
// trailing whitespace. Trailing blank lines (common after a command's output)
// are skipped so the heuristic judges the prompt line, not the empty rows below
// it.
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

// paneObservation is the tracker's per-session memory: the last visible pane
// text, when it last changed, and the status last derived for it.
type paneObservation struct {
	visible   string
	changedAt time.Time
	status    runtimeStatus
}

// statusTracker derives and remembers a runtime status per session from a
// stream of pane snapshots. It is the runtime layer the issue calls for: it
// lives entirely in memory beside the registry and touches no persisted state.
// now is injectable so tests can drive the working window deterministically.
type statusTracker struct {
	now    func() time.Time
	window time.Duration
	obs    map[string]*paneObservation
}

func newStatusTracker(now func() time.Time) *statusTracker {
	return &statusTracker{
		now:    now,
		window: workingWindow,
		obs:    make(map[string]*paneObservation),
	}
}

// observe feeds a fresh pane capture for session id and returns the status
// derived from it together with the status the session held before this
// observation. The first observation of a session reports a previous status of
// statusUnknown, so a freshly tracked session never looks like a transition.
// raw may carry escape sequences; only the visible text drives change
// detection.
func (t *statusTracker) observe(id, raw string) (now, prev runtimeStatus) {
	visible := strings.TrimRight(ansi.Strip(raw), " \t\n")
	at := t.now()

	o := t.obs[id]
	if o == nil {
		t.obs[id] = &paneObservation{
			visible:   visible,
			changedAt: at,
			status:    statusWorking,
		}
		return statusWorking, statusUnknown
	}

	if visible != o.visible {
		o.visible = visible
		o.changedAt = at
	}
	prev = o.status
	o.status = deriveStatus(visible, at.Sub(o.changedAt), t.window)
	return o.status, prev
}

// markExited records that a session has exited, returning the status it held
// before so a running→exited transition can be detected. An untracked session
// reports statusUnknown as its previous status.
func (t *statusTracker) markExited(id string) (prev runtimeStatus) {
	o := t.obs[id]
	if o == nil {
		t.obs[id] = &paneObservation{status: statusExited, changedAt: t.now()}
		return statusUnknown
	}
	prev = o.status
	o.status = statusExited
	return prev
}

// status returns the status currently tracked for id, or statusUnknown when the
// session has not been observed yet.
func (t *statusTracker) status(id string) runtimeStatus {
	if o := t.obs[id]; o != nil {
		return o.status
	}
	return statusUnknown
}

// forget drops every tracked session whose id is not in keep, so the tracker
// does not accumulate state for sessions removed from the registry.
func (t *statusTracker) forget(keep map[string]bool) {
	for id := range t.obs {
		if !keep[id] {
			delete(t.obs, id)
		}
	}
}

// makeNotifier returns the side-effecting notifier for a notify mode. off is a
// no-op; bell rings the terminal bell on stderr (a non-printing byte that does
// not disturb Bubble Tea's stdout renderer); os shells out to the host's
// desktop notifier off the caller's goroutine so a slow spawn never blocks the
// UI. An unknown mode degrades to silence.
func makeNotifier(mode config.Notify) func(title, body string) {
	switch mode {
	case config.NotifyBell:
		return func(string, string) { _, _ = os.Stderr.WriteString("\a") }
	case config.NotifyOS:
		return func(title, body string) { go osNotify(title, body) }
	default:
		return func(string, string) {}
	}
}

// osNotify posts a desktop notification through the host's notifier:
// osascript on macOS, notify-send elsewhere (Linux/BSD). Any failure — the tool
// missing, a non-graphical session — is swallowed: a notification is a
// convenience, never a hard dependency.
func osNotify(title, body string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		script := "display notification " + quoteOSA(body) +
			" with title " + quoteOSA(title)
		cmd = exec.Command("osascript", "-e", script)
	default:
		cmd = exec.Command("notify-send", title, body)
	}
	_ = cmd.Run()
}

// quoteOSA wraps s as an AppleScript string literal, escaping backslashes and
// quotes so a session title containing them cannot break out of the script.
func quoteOSA(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return "\"" + s + "\""
}
