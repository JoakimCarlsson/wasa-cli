package tui

import (
	"fmt"
	"strings"

	"github.com/joakimcarlsson/wasa/internal/config"
)

// recordEditor captures a key binding by listening for keypresses: each key the
// user presses is appended to the binding, like rebinding a control in a game.
// It knows the keys bound to every other action so it can warn the instant a
// captured key collides, before save validation would reject it. enter, esc and
// backspace are reserved by the editor as commit/cancel/remove, so they cannot be
// recorded here.
type recordEditor struct {
	th     Theme
	action string
	keys   []string
	other  map[string]string // key -> the other action that already binds it
	warn   string
}

func newRecordEditor(th Theme, action string, working config.Config) recordEditor {
	other := make(map[string]string)
	for a, ks := range working.Keys {
		if a == action {
			continue
		}
		for _, k := range ks {
			other[k] = a
		}
	}
	return recordEditor{th: th, action: action, other: other}
}

// add records a pressed key, ignoring an exact repeat of the last one, and warns
// when the key is already bound to another action.
func (e recordEditor) add(key string) recordEditor {
	if n := len(e.keys); n > 0 && e.keys[n-1] == key {
		return e
	}
	e.keys = append(e.keys, key)
	if a, ok := e.other[key]; ok {
		e.warn = fmt.Sprintf("%q is also bound to %q", key, a)
	} else {
		e.warn = ""
	}
	return e
}

func (e recordEditor) removeLast() recordEditor {
	if len(e.keys) > 0 {
		e.keys = e.keys[:len(e.keys)-1]
	}
	e.warn = ""
	return e
}

// value renders the recorded keys for commit as the comma-separated list the key
// field setter parses.
func (e recordEditor) value() string { return strings.Join(e.keys, ", ") }

func (e recordEditor) view() string {
	captured := e.th.dimStyle.Render("(press a key)")
	if len(e.keys) > 0 {
		labels := make([]string, len(e.keys))
		for i, k := range e.keys {
			labels[i] = e.th.matchStyle.Render(keyLabel(k))
		}
		captured = strings.Join(labels, " ")
	}

	lines := []string{
		e.th.titleStyle.Render("Bind " + e.action),
		"",
		captured,
	}
	if e.warn != "" {
		lines = append(lines, "", e.th.errorStyle.Render(e.warn))
	}
	return strings.Join(lines, "\n")
}
