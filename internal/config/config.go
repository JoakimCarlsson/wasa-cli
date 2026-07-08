package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const fileName = "config.json"

// Config is the resolved cockpit configuration: the built-in defaults overlaid
// with whatever the user's config.json specified. The zero value is not usable;
// obtain one from Default or Load. Path records where the file was looked for,
// for the `wasa config` affordance, and is never serialised.
type Config struct {
	Theme   Theme   `json:"theme"`
	Keys    Keys    `json:"keys"`
	Layout  Layout  `json:"layout"`
	Notify  Notify  `json:"notify"`
	History History `json:"history"`

	Path string `json:"-"`
}

// History controls injecting recorded history from prior checkpoints into a new
// session's starting context. Enabled by default; MaxBytes caps the injected
// block so it never crowds out the task the session was launched for.
type History struct {
	Enabled  bool `json:"enabled"`
	MaxBytes int  `json:"maxBytes"`
}

// Notify selects how the cockpit announces a session that transitions into
// needing attention (waiting for input) or finishing (exited) while it is not
// the focused session. It is deliberately small: silent, a terminal bell, or a
// desktop notification through the host's notifier.
type Notify string

// Notification modes. NotifyBell is the conservative default — a contained
// terminal bell that spawns no external process and, debounced, cannot spam.
const (
	NotifyOff  Notify = "off"
	NotifyBell Notify = "bell"
	NotifyOS   Notify = "os"
)

// Color is a single palette entry. It may carry distinct light- and dark-terminal
// variants; the cockpit renders the one matching the terminal background. In the
// config file a colour may be written either as a bare string (applied to both
// variants) or as an object with light and/or dark keys; an object that omits one
// variant leaves that variant at its default.
type Color struct {
	Light string `json:"light"`
	Dark  string `json:"dark"`
}

// UnmarshalJSON accepts both the bare-string and the {light,dark} object forms.
// The string form sets both variants. The object form merges over the receiver's
// current value, so a partial object keeps the unspecified variant's default.
func (c *Color) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		c.Light, c.Dark = s, s
		return nil
	}
	type alias Color
	a := alias(*c)
	if err := json.Unmarshal(b, &a); err != nil {
		return fmt.Errorf("invalid colour %s: %w", b, err)
	}
	*c = Color(a)
	return nil
}

// Theme is the cockpit's full palette. Each field corresponds to a styled
// element: the accent drives borders, the active tab and selection; running and
// exited colour the status dots; title and desc are the row text greys; selection
// fg/bg are the highlighted-row colours; danger is the destructive accent; onAccent
// is the text laid over an accent fill (tabs, buttons); inactiveBtnBg is the
// unfocused button fill; and the menu greys colour the footer hint bar.
type Theme struct {
	Accent        Color `json:"accent"`
	Running       Color `json:"running"`
	Waiting       Color `json:"waiting"`
	Idle          Color `json:"idle"`
	Exited        Color `json:"exited"`
	Title         Color `json:"title"`
	Desc          Color `json:"desc"`
	SelectionFg   Color `json:"selectionFg"`
	SelectionBg   Color `json:"selectionBg"`
	DiffAddBg     Color `json:"diffAddBg"`
	DiffDelBg     Color `json:"diffDelBg"`
	Danger        Color `json:"danger"`
	OnAccent      Color `json:"onAccent"`
	InactiveBtnBg Color `json:"inactiveBtnBg"`
	MenuKey       Color `json:"menuKey"`
	MenuDesc      Color `json:"menuDesc"`
	MenuSep       Color `json:"menuSep"`
}

// Layout controls the cockpit's column sizing. ListColFrac is the fraction of the
// terminal width given to the session list; MinListWidth is the floor that keeps
// the list usable on a narrow terminal; CompactWidth and CompactHeight are the
// thresholds below which the cockpit drops to its single-column compact frame.
type Layout struct {
	ListColFrac   float64 `json:"listColFrac"`
	MinListWidth  int     `json:"minListWidth"`
	CompactWidth  int     `json:"compactWidth"`
	CompactHeight int     `json:"compactHeight"`
}

// KeyList is the set of keys bound to one action. In the config file it may be
// written as a single key string or as an array of key strings.
type KeyList []string

// UnmarshalJSON accepts both the single-string and array forms.
func (k *KeyList) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*k = KeyList{s}
		return nil
	}
	var arr []string
	if err := json.Unmarshal(b, &arr); err != nil {
		return fmt.Errorf("invalid key binding %s: %w", b, err)
	}
	*k = arr
	return nil
}

// Keys maps a cockpit action name to the keys that trigger it. A user file
// overrides individual actions; any action it omits keeps its default binding.
type Keys map[string]KeyList

// Action names. These are the stable identifiers a user writes under the "keys"
// section of the config file.
const (
	ActionNew             = "new"
	ActionAttach          = "attach"
	ActionKill            = "kill"
	ActionDelete          = "delete"
	ActionPause           = "pause"
	ActionResume          = "resume"
	ActionTabNext         = "tab-next"
	ActionTabPrev         = "tab-prev"
	ActionPaneTab         = "pane-tab"
	ActionCursorUp        = "cursor-up"
	ActionCursorDown      = "cursor-down"
	ActionFilter          = "filter"
	ActionWorkspaceAdd    = "workspace-add"
	ActionWorkspaceDelete = "workspace-delete"
	ActionRecordToggle    = "record-toggle"
	ActionConfig          = "config"
	ActionQuit            = "quit"
)

// modeList is the cockpit's session-list mode. Duplicate-binding detection is
// scoped to a mode, so the same key may drive different actions in different
// modes (e.g. enter attaches in the list but submits in the create form).
const modeList = "list"

// binding pairs an action with the mode it belongs to and its default keys. It is
// the single source of truth for the action vocabulary: Default builds the
// default keymap from it and validate checks user actions against it.
type binding struct {
	action string
	mode   string
	keys   KeyList
}

var defaultBindings = []binding{
	{ActionNew, modeList, KeyList{"n"}},
	{ActionAttach, modeList, KeyList{"enter"}},
	{ActionKill, modeList, KeyList{"k"}},
	{ActionDelete, modeList, KeyList{"d"}},
	{ActionPause, modeList, KeyList{"p"}},
	{ActionResume, modeList, KeyList{"r"}},
	{ActionTabNext, modeList, KeyList{"tab", "right", "]"}},
	{ActionTabPrev, modeList, KeyList{"shift+tab", "left", "["}},
	{ActionPaneTab, modeList, KeyList{"ctrl+t"}},
	{ActionCursorUp, modeList, KeyList{"up"}},
	{ActionCursorDown, modeList, KeyList{"down"}},
	{ActionFilter, modeList, KeyList{"ctrl+f"}},
	{ActionWorkspaceAdd, modeList, KeyList{"w"}},
	{ActionWorkspaceDelete, modeList, KeyList{"W"}},
	{ActionRecordToggle, modeList, KeyList{"R"}},
	{ActionConfig, modeList, KeyList{","}},
	{ActionQuit, modeList, KeyList{"q", "ctrl+c"}},
}

// Default returns the built-in configuration: the historical palette, key
// bindings and layout. An absent or partial user file resolves over this, so the
// defaults define wasa's out-of-the-box behaviour.
func Default() Config {
	return Config{
		Theme: Theme{
			Accent:        Color{Light: "#874BFD", Dark: "#7D56F4"},
			Running:       both("#51bd73"),
			Waiting:       both("#e0af68"),
			Idle:          both("#56b6c2"),
			Exited:        both("#888888"),
			Title:         Color{Light: "#1a1a1a", Dark: "#dddddd"},
			Desc:          Color{Light: "#A49FA5", Dark: "#777777"},
			SelectionFg:   both("#1a1a1a"),
			SelectionBg:   both("#dde4f0"),
			DiffAddBg:     Color{Light: "#e6ffec", Dark: "#1d2b22"},
			DiffDelBg:     Color{Light: "#ffebe9", Dark: "#33232a"},
			Danger:        both("#de613e"),
			OnAccent:      both("230"),
			InactiveBtnBg: both("236"),
			MenuKey:       Color{Light: "#655F5F", Dark: "#cfcaca"},
			MenuDesc:      Color{Light: "#7A7474", Dark: "#9C9494"},
			MenuSep:       Color{Light: "#DDDADA", Dark: "#3C3C3C"},
		},
		Layout: Layout{
			ListColFrac:   0.34,
			MinListWidth:  24,
			CompactWidth:  40,
			CompactHeight: 8,
		},
		Keys:    defaultKeys(),
		Notify:  NotifyBell,
		History: History{Enabled: true, MaxBytes: 6000},
	}
}

func both(s string) Color { return Color{Light: s, Dark: s} }

func defaultKeys() Keys {
	k := make(Keys, len(defaultBindings))
	for _, b := range defaultBindings {
		keys := make(KeyList, len(b.keys))
		copy(keys, b.keys)
		k[b.action] = keys
	}
	return k
}

// Load resolves the configuration stored under dir. A missing config.json is not
// an error: it yields the defaults. A present file is overlaid onto the defaults
// field-by-field, so a partial file leaves unspecified settings at their default.
// Malformed JSON, an unknown action, a conflicting binding or an out-of-range
// layout value is returned as an error so the cockpit refuses to start on a bad
// config rather than falling back silently.
func Load(dir string) (Config, error) {
	cfg := Default()
	path := filepath.Join(dir, fileName)
	cfg.Path = path

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, err
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

// validate rejects a resolved config that the cockpit cannot honour: an unknown
// action name, the same key bound to two actions in one mode, or a layout value
// outside its sane range.
func (c Config) validate() error {
	mode := make(map[string]string, len(defaultBindings))
	for _, b := range defaultBindings {
		mode[b.action] = b.mode
	}

	seen := make(map[string]map[string]string)
	for _, action := range sortedActions(c.Keys) {
		m, ok := mode[action]
		if !ok {
			return fmt.Errorf(
				"unknown key action %q (valid actions: %s)",
				action, strings.Join(validActions(), ", "),
			)
		}
		if seen[m] == nil {
			seen[m] = make(map[string]string)
		}
		for _, key := range c.Keys[action] {
			if other, dup := seen[m][key]; dup && other != action {
				return fmt.Errorf(
					"key %q is bound to both %q and %q", key, other, action,
				)
			}
			seen[m][key] = action
		}
	}

	if err := ValidateNotify(c.Notify); err != nil {
		return err
	}
	if c.History.MaxBytes < 0 {
		return fmt.Errorf("history.maxBytes must not be negative")
	}
	return c.Layout.validate()
}

// ValidateNotify rejects a notify mode the cockpit does not recognise, so a typo
// in the config file — or in the in-cockpit editor — is reported rather than
// silently disabling notifications.
func ValidateNotify(n Notify) error {
	switch n {
	case NotifyOff, NotifyBell, NotifyOS:
		return nil
	default:
		return fmt.Errorf(
			"notify must be one of %q, %q, %q, got %q",
			NotifyOff, NotifyBell, NotifyOS, n,
		)
	}
}

func (l Layout) validate() error {
	if l.ListColFrac <= 0 || l.ListColFrac >= 1 {
		return fmt.Errorf(
			"layout.listColFrac must be between 0 and 1, got %g", l.ListColFrac,
		)
	}
	if l.MinListWidth <= 0 {
		return fmt.Errorf(
			"layout.minListWidth must be positive, got %d", l.MinListWidth,
		)
	}
	if l.CompactWidth <= 0 {
		return fmt.Errorf(
			"layout.compactWidth must be positive, got %d", l.CompactWidth,
		)
	}
	if l.CompactHeight <= 0 {
		return fmt.Errorf(
			"layout.compactHeight must be positive, got %d", l.CompactHeight,
		)
	}
	return nil
}

// Save validates c and writes it to config.json under dir. It writes to a
// temporary file in the same directory and renames it into place so a partial
// write never corrupts an existing config. A config that fails validation is
// rejected before any write, so the on-disk file is always loadable.
func Save(dir string, c Config) error {
	if err := c.validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, fileName+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, Path(dir))
}

// Path returns the config file location under a given $WASA_HOME dir.
func Path(dir string) string { return filepath.Join(dir, fileName) }

// Actions returns the cockpit action names in their canonical order, for a UI
// that enumerates the bindable actions (such as the in-cockpit config editor).
func Actions() []string {
	out := make([]string, len(defaultBindings))
	for i, b := range defaultBindings {
		out[i] = b.action
	}
	return out
}

func sortedActions(k Keys) []string {
	out := make([]string, 0, len(k))
	for a := range k {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

func validActions() []string { return Actions() }
