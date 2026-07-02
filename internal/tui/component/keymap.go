package component

import "github.com/joakimcarlsson/wasa-cli/internal/config"

// Keymap resolves a key press to a cockpit action and reports the effective key
// for an action so the menu can render the binding actually in force. It is built
// once from the resolved config, so a remapped binding drives both key handling
// and the displayed hints. Duplicate bindings are rejected earlier, in
// config.validate, so the key→action map here is unambiguous.
type Keymap struct {
	byKey map[string]string
	keys  config.Keys
}

// NewKeymap builds a Keymap from the resolved key bindings, inverting the
// action→keys map into the key→action lookup used at key-press time.
func NewKeymap(keys config.Keys) Keymap {
	byKey := make(map[string]string, len(keys))
	for action, ks := range keys {
		for _, k := range ks {
			byKey[k] = action
		}
	}
	return Keymap{byKey: byKey, keys: keys}
}

// Action returns the action bound to key, or "" when key is unbound.
func (km Keymap) Action(key string) string { return km.byKey[key] }

// Primary returns the representative key for an action — the first one bound to
// it — used to label the action in the menu. It is "" when the action is unbound.
func (km Keymap) Primary(action string) string {
	if ks := km.keys[action]; len(ks) > 0 {
		return ks[0]
	}
	return ""
}

// KeyLabel renders a key string as the glyph shown in the menu bar. Known keys
// get their conventional symbol (↵, ⇥, the arrows); a ctrl chord shows as ^x; any
// other key is shown verbatim.
func KeyLabel(key string) string {
	switch key {
	case "":
		return ""
	case "enter":
		return "↵"
	case "tab":
		return "⇥"
	case "shift+tab":
		return "⇧⇥"
	case "up":
		return "↑"
	case "down":
		return "↓"
	case "left":
		return "←"
	case "right":
		return "→"
	case "esc":
		return "esc"
	case "space":
		return "␣"
	}
	if len(key) > 5 && key[:5] == "ctrl+" {
		return "^" + key[5:]
	}
	return key
}
