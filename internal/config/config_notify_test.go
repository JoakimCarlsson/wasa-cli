package config

import (
	"strings"
	"testing"
)

func TestDefaultNotifyIsBell(t *testing.T) {
	if got := Default().Notify; got != NotifyBell {
		t.Fatalf("default notify = %q, want %q", got, NotifyBell)
	}
}

func TestLoadNotifyOverride(t *testing.T) {
	dir := writeConfig(t, `{"notify": "os"}`)
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Notify != NotifyOS {
		t.Fatalf("notify = %q, want %q", got.Notify, NotifyOS)
	}
}

func TestLoadAbsentNotifyKeepsDefault(t *testing.T) {
	dir := writeConfig(t, `{"layout": {"minListWidth": 30}}`)
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Notify != NotifyBell {
		t.Fatalf("notify = %q, want default %q", got.Notify, NotifyBell)
	}
}

func TestLoadInvalidNotifyErrors(t *testing.T) {
	dir := writeConfig(t, `{"notify": "popup"}`)
	if _, err := Load(dir); err == nil {
		t.Fatal("Load accepted an unknown notify mode")
	} else if !strings.Contains(err.Error(), "notify") {
		t.Fatalf("error did not mention notify: %v", err)
	}
}

func TestValidateNotify(t *testing.T) {
	for _, n := range []Notify{NotifyOff, NotifyBell, NotifyOS} {
		if err := ValidateNotify(n); err != nil {
			t.Errorf("ValidateNotify(%q) = %v, want nil", n, err)
		}
	}
	if err := ValidateNotify(Notify("nope")); err == nil {
		t.Error("ValidateNotify accepted an unknown mode")
	}
}
