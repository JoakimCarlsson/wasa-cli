package component

import (
	"testing"

	"github.com/joakimcarlsson/wasa-cli/internal/config"
)

func TestNewKeymapResolvesDefaults(t *testing.T) {
	km := NewKeymap(config.Default().Keys)

	cases := map[string]string{
		"n":      config.ActionNew,
		"enter":  config.ActionAttach,
		"k":      config.ActionKill,
		"d":      config.ActionDelete,
		"tab":    config.ActionTabNext,
		"]":      config.ActionTabNext,
		"left":   config.ActionTabPrev,
		"up":     config.ActionCursorUp,
		"down":   config.ActionCursorDown,
		"q":      config.ActionQuit,
		"ctrl+c": config.ActionQuit,
	}
	for key, want := range cases {
		if got := km.Action(key); got != want {
			t.Errorf("Action(%q) = %q, want %q", key, got, want)
		}
	}
	if got := km.Action("z"); got != "" {
		t.Errorf("unbound key resolved to %q", got)
	}
}

func TestNewKeymapHonoursOverride(t *testing.T) {
	keys := config.Default().Keys
	keys[config.ActionNew] = config.KeyList{"x"}
	km := NewKeymap(keys)

	if got := km.Action("x"); got != config.ActionNew {
		t.Errorf("remapped key x = %q, want new", got)
	}
	if got := km.Action("n"); got == config.ActionNew {
		t.Error("old key n still triggers new after remap")
	}
	if got := km.Primary(config.ActionNew); got != "x" {
		t.Errorf("Primary(new) = %q, want x", got)
	}
}
