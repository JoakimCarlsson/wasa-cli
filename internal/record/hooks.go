package record

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// hookMarker identifies a hook entry as wasa's recorder, so install is
// idempotent, coexisting worktree-level and repo-level installs merge to one
// entry, and removal never touches a user's own hooks. It must not be a
// substring of the sessionstatus handler's marker ("hook-handler") or vice
// versa.
const hookMarker = "record-hook"

// HookCommand returns the handler invocation installed into an agent's hook
// configuration. exe is the absolute wasa binary path; end marks the entry
// installed on the agent's session-end event, so the handler knows to close
// the record without a per-agent event vocabulary.
func HookCommand(exe, tool string, end bool) string {
	cmd := exe + " " + hookMarker + " --tool " + tool
	if end {
		cmd += " --event end"
	}
	return cmd
}

// settingsHookEntry is one installed hook command. The optional fields cover
// every supported agent's dialect: Gemini requires a name, Codex accepts a
// timeout, Copilot runs a bash field instead of command.
type settingsHookEntry struct {
	Name    string `json:"name,omitempty"`
	Type    string `json:"type,omitempty"`
	Command string `json:"command,omitempty"`
	Bash    string `json:"bash,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

type settingsHookMatcher struct {
	Matcher string              `json:"matcher,omitempty"`
	Hooks   []settingsHookEntry `json:"hooks"`
}

// entryMaker builds an agent's dialect of a hook entry for a command.
// nil means the plain Claude-style {"type":"command","command":...}.
type entryMaker func(command string) settingsHookEntry

func plainEntry(command string) settingsHookEntry {
	return settingsHookEntry{Type: "command", Command: command}
}

func geminiEntry(command string) settingsHookEntry {
	return settingsHookEntry{
		Name:    "wasa-record",
		Type:    "command",
		Command: command,
	}
}

func codexEntry(command string) settingsHookEntry {
	return settingsHookEntry{Type: "command", Command: command, Timeout: 30}
}

func cursorEntry(command string) settingsHookEntry {
	return settingsHookEntry{Command: command}
}

// settingsFile joins an agent config path under dir and remembers it
// relative to dir for the info/exclude entry.
type configFile struct {
	path    string
	exclude string
}

func settingsFile(dir, sub, name string) configFile {
	return configFile{
		path:    filepath.Join(dir, sub, name),
		exclude: filepath.ToSlash(filepath.Join(sub, name)),
	}
}

// installNested merges wasa's recording hooks into a Claude-style settings
// file: {"hooks":{"<Event>":[{"hooks":[<entry>]}]}}. It is additive and
// idempotent: user settings and hooks are preserved, and a re-install is a
// no-op once a recorder entry is present. A file that exists but is not
// valid JSON is left untouched and the error returned, so a hand-edited
// config is never clobbered. The file is added to the repository's shared
// info/exclude (best-effort) so recording never dirties git status. finish
// is an optional extra mutation of the top-level document (e.g. Gemini's
// hooksConfig.enabled).
func installNested(
	f configFile,
	repoDir, tool, wasaExe string,
	events []hookEvent,
	entry entryMaker,
	finish func(top map[string]json.RawMessage),
) error {
	if entry == nil {
		entry = plainEntry
	}
	top, hooks, err := loadNested(f.path)
	if err != nil {
		return err
	}
	changed := false
	for _, event := range events {
		if hasRecordHook(hooks[event.name]) {
			continue
		}
		hooks[event.name] = append(hooks[event.name], settingsHookMatcher{
			Hooks: []settingsHookEntry{
				entry(HookCommand(wasaExe, tool, event.end)),
			},
		})
		changed = true
	}
	if changed {
		rawHooks, err := json.Marshal(hooks)
		if err != nil {
			return err
		}
		top["hooks"] = rawHooks
		if finish != nil {
			finish(top)
		}
		if err := writeJSONFile(f.path, top); err != nil {
			return err
		}
	}
	ensureExcluded(repoDir, f.exclude)
	return nil
}

// removeNested strips wasa's recording entries from a Claude-style settings
// file, leaving every other setting and hook in place. ownedTop lists
// top-level keys the installer added besides hooks; they are dropped when
// they are all that remains, and the file is deleted when nothing else is
// left. A missing file is a no-op.
func removeNested(f configFile, ownedTop []string) error {
	if _, err := os.Stat(f.path); os.IsNotExist(err) {
		return nil
	}
	top, hooks, err := loadNested(f.path)
	if err != nil {
		return err
	}
	changed := false
	for event, matchers := range hooks {
		var kept []settingsHookMatcher
		for _, m := range matchers {
			if matcherIsRecordHook(m) {
				changed = true
				continue
			}
			kept = append(kept, m)
		}
		if len(kept) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = kept
		}
	}
	if !changed {
		return nil
	}
	if len(hooks) == 0 {
		delete(top, "hooks")
	} else {
		rawHooks, err := json.Marshal(hooks)
		if err != nil {
			return err
		}
		top["hooks"] = rawHooks
	}
	if len(hooks) == 0 && onlyOwnedKeys(top, ownedTop) {
		if err := os.Remove(f.path); err != nil {
			return err
		}
		_ = os.Remove(filepath.Dir(f.path))
		return nil
	}
	return writeJSONFile(f.path, top)
}

func nestedInstalled(f configFile) bool {
	_, hooks, err := loadNested(f.path)
	if err != nil {
		return false
	}
	for _, matchers := range hooks {
		if hasRecordHook(matchers) {
			return true
		}
	}
	return false
}

// installFlat writes wasa's recording hooks in the flat version-1 shape
// Copilot and Cursor discover: {"version":1,"hooks":{"<event>":[<entry>]}}.
// wasa owns the whole file (it is named after wasa or lives in a discovery
// directory), so install rewrites it and remove deletes it; a foreign file
// without wasa's marker is left untouched and reported.
func installFlat(
	f configFile,
	repoDir, tool, wasaExe string,
	events []hookEvent,
	entry entryMaker,
) error {
	if data, err := os.ReadFile(f.path); err == nil &&
		!strings.Contains(string(data), hookMarker) {
		return fmt.Errorf(
			"%s exists and is not wasa's; not overwriting",
			f.path,
		)
	}
	hooks := map[string][]settingsHookEntry{}
	for _, event := range events {
		hooks[event.name] = []settingsHookEntry{
			entry(HookCommand(wasaExe, tool, event.end)),
		}
	}
	rawHooks, err := json.Marshal(hooks)
	if err != nil {
		return err
	}
	top := map[string]json.RawMessage{
		"version": json.RawMessage("1"),
		"hooks":   rawHooks,
	}
	if err := writeJSONFile(f.path, top); err != nil {
		return err
	}
	ensureExcluded(repoDir, f.exclude)
	return nil
}

func removeFlat(f configFile) error {
	data, err := os.ReadFile(f.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), hookMarker) {
		return nil
	}
	if err := os.Remove(f.path); err != nil {
		return err
	}
	_ = os.Remove(filepath.Dir(f.path))
	return nil
}

func flatInstalled(f configFile) bool {
	data, err := os.ReadFile(f.path)
	return err == nil && strings.Contains(string(data), hookMarker)
}

// copilotHookFile is wasa's recorder hook in the repository's .github/hooks
// directory — the repo-level hook location Copilot reads (alongside any other
// .github/hooks/*.json). The file is wasa-owned and distinctly named, so
// installFlat rewrites it, removeFlat deletes it, and a foreign file without
// wasa's marker is left untouched. It is added to info/exclude like every other
// recorder config so recording never dirties git status.
func copilotHookFile(dir string) configFile {
	return settingsFile(dir, filepath.Join(".github", "hooks"), "wasa.json")
}

// copilotEntry is a Copilot repo-hook entry: {"type":"command","command":...}.
func copilotEntry(command string) settingsHookEntry {
	return settingsHookEntry{Type: "command", Command: command}
}

// codexFeatureFlag is the config.toml content Codex needs before it runs
// hooks at all. wasa carries no TOML parser, so it only creates the file
// when missing and only deletes it when unchanged; an existing config.toml
// the user owns is never edited.
const codexFeatureFlag = "[features]\nhooks = true\n"

func codexConfigPath(dir string) string {
	return filepath.Join(dir, ".codex", "config.toml")
}

func ensureCodexFeature(dir string) error {
	path := codexConfigPath(dir)
	data, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(
			path,
			[]byte(codexFeatureFlag),
			0o644,
		); err != nil {
			return err
		}
		ensureExcluded(dir, ".codex/config.toml")
		return nil
	case err != nil:
		return err
	case strings.Contains(string(data), "hooks = true"),
		strings.Contains(string(data), "hooks=true"):
		return nil
	default:
		return fmt.Errorf(
			"codex hooks are disabled: add \"[features]\\nhooks = true\" to %s",
			path,
		)
	}
}

func removeCodexFeature(dir string) {
	path := codexConfigPath(dir)
	if data, err := os.ReadFile(path); err == nil &&
		string(data) == codexFeatureFlag {
		_ = os.Remove(path)
		_ = os.Remove(filepath.Dir(path))
	}
}

// enableGeminiHooks sets hooksConfig.enabled, without which Gemini ignores
// the hooks section entirely.
func enableGeminiHooks(top map[string]json.RawMessage) {
	cfg := map[string]json.RawMessage{}
	if raw, ok := top["hooksConfig"]; ok {
		_ = json.Unmarshal(raw, &cfg)
	}
	cfg["enabled"] = json.RawMessage("true")
	if raw, err := json.Marshal(cfg); err == nil {
		top["hooksConfig"] = raw
	}
}

// loadNested parses a settings file into its top-level raw fields and its
// decoded hooks map. A missing or empty file yields empty maps.
func loadNested(path string) (
	map[string]json.RawMessage, map[string][]settingsHookMatcher, error,
) {
	top := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return nil, nil, err
	case len(strings.TrimSpace(string(data))) > 0:
		if err := json.Unmarshal(data, &top); err != nil {
			return nil, nil, fmt.Errorf(
				"settings %s is not valid JSON; leaving it untouched: %w",
				path, err,
			)
		}
	}
	hooks := map[string][]settingsHookMatcher{}
	if raw, ok := top["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, nil, fmt.Errorf(
				"settings %s has malformed hooks: %w", path, err,
			)
		}
	}
	return top, hooks, nil
}

// writeJSONFile re-encodes a settings document atomically.
func writeJSONFile(path string, top map[string]json.RawMessage) error {
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".wasa-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

func onlyOwnedKeys(top map[string]json.RawMessage, owned []string) bool {
	for key := range top {
		if !slices.Contains(owned, key) {
			return false
		}
	}
	return true
}

func hasRecordHook(matchers []settingsHookMatcher) bool {
	return slices.ContainsFunc(matchers, matcherIsRecordHook)
}

func matcherIsRecordHook(m settingsHookMatcher) bool {
	for _, h := range m.Hooks {
		if strings.Contains(h.Command, hookMarker) ||
			strings.Contains(h.Bash, hookMarker) {
			return true
		}
	}
	return false
}

// ensureExcluded appends entry (a repo-root-relative path) to the
// repository's shared info/exclude when missing, so installed hook
// configuration never dirties git status or blocks a clean worktree
// removal. Tracked files are unaffected by exclude; a repository that
// commits its agent settings keeps full control of them. Best-effort: any
// failure is ignored, costing only an untracked-file line in git status.
func ensureExcluded(dir, entry string) {
	commonDir, err := gitIn(
		dir, nil, "rev-parse", "--path-format=absolute", "--git-common-dir",
	)
	if err != nil || commonDir == "" {
		return
	}
	path := filepath.Join(commonDir, "info", "exclude")
	data, _ := os.ReadFile(path)
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(line) == entry {
			return
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	prefix := ""
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		prefix = "\n"
	}
	fmt.Fprintf(f, "%s%s\n", prefix, entry)
}
