package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeConfig writes contents to a config.json under a fresh temp dir and returns
// that dir, the WASA_HOME the cockpit would Load from.
func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, fileName), []byte(contents), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadAbsentFileYieldsDefaults(t *testing.T) {
	dir := t.TempDir()
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := Default()
	want.Path = filepath.Join(dir, fileName)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("absent file did not resolve to defaults\n got: %+v\nwant: %+v", got, want)
	}
}

func TestLoadPartialThemeKeepsDefaults(t *testing.T) {
	dir := writeConfig(t, `{"theme":{"accent":"#ff0000"}}`)
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.Theme.Accent != (Color{Light: "#ff0000", Dark: "#ff0000"}) {
		t.Errorf("accent not overridden: %+v", got.Theme.Accent)
	}
	def := Default()
	if got.Theme.Running != def.Theme.Running {
		t.Errorf("unspecified colour changed: %+v", got.Theme.Running)
	}
	if got.Theme.Title != def.Theme.Title {
		t.Errorf("unspecified adaptive colour changed: %+v", got.Theme.Title)
	}
}

func TestLoadPartialAdaptiveColorKeepsOtherVariant(t *testing.T) {
	dir := writeConfig(t, `{"theme":{"accent":{"dark":"#123456"}}}`)
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	def := Default()
	want := Color{Light: def.Theme.Accent.Light, Dark: "#123456"}
	if got.Theme.Accent != want {
		t.Errorf("partial adaptive colour: got %+v want %+v", got.Theme.Accent, want)
	}
}

func TestLoadPartialKeysKeepDefaults(t *testing.T) {
	dir := writeConfig(t, `{"keys":{"new":"N"}}`)
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !reflect.DeepEqual(got.Keys[ActionNew], KeyList{"N"}) {
		t.Errorf("new not remapped: %+v", got.Keys[ActionNew])
	}
	if !reflect.DeepEqual(got.Keys[ActionKill], KeyList{"k"}) {
		t.Errorf("unbound action changed: %+v", got.Keys[ActionKill])
	}
}

func TestLoadKeysAcceptArrayForm(t *testing.T) {
	dir := writeConfig(t, `{"keys":{"new":["N","ctrl+n"]}}`)
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got.Keys[ActionNew], KeyList{"N", "ctrl+n"}) {
		t.Errorf("array binding: %+v", got.Keys[ActionNew])
	}
}

func TestLoadPartialLayoutKeepsDefaults(t *testing.T) {
	dir := writeConfig(t, `{"layout":{"listColFrac":0.5}}`)
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Layout.ListColFrac != 0.5 {
		t.Errorf("listColFrac not overridden: %v", got.Layout.ListColFrac)
	}
	if got.Layout.MinListWidth != Default().Layout.MinListWidth {
		t.Errorf("unspecified layout value changed: %v", got.Layout.MinListWidth)
	}
}

func TestLoadMalformedJSONErrors(t *testing.T) {
	dir := writeConfig(t, `{"theme": `)
	if _, err := Load(dir); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestLoadUnknownActionErrors(t *testing.T) {
	dir := writeConfig(t, `{"keys":{"frobnicate":"x"}}`)
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func TestLoadDuplicateBindingErrors(t *testing.T) {
	dir := writeConfig(t, `{"keys":{"new":"k"}}`)
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error: new and kill both bound to k")
	}
}

func TestLoadDuplicateWithinOneActionIsAllowed(t *testing.T) {
	dir := writeConfig(t, `{"keys":{"new":["n","n"]}}`)
	if _, err := Load(dir); err != nil {
		t.Fatalf("same key repeated in one action should be allowed: %v", err)
	}
}

func TestLoadOutOfRangeLayoutErrors(t *testing.T) {
	for _, body := range []string{
		`{"layout":{"listColFrac":0}}`,
		`{"layout":{"listColFrac":1}}`,
		`{"layout":{"minListWidth":0}}`,
		`{"layout":{"compactHeight":-1}}`,
	} {
		dir := writeConfig(t, body)
		if _, err := Load(dir); err == nil {
			t.Errorf("expected error for %s", body)
		}
	}
}
