package tui

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/joakimcarlsson/wasa-cli/internal/config"
)

// notifyDebounce is the minimum gap between two notifications for the same
// session, so a session that flaps between states cannot produce a burst.
const notifyDebounce = 5 * time.Second

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

// osNotify posts a desktop notification through the host's notifier: osascript
// on macOS, notify-send elsewhere. Any failure — the tool missing, a
// non-graphical session — is swallowed: a notification is a convenience, never a
// hard dependency.
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
