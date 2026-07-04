package record

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// hookMarker identifies a settings hook entry as wasa's recorder, so install
// is idempotent, coexisting worktree-level and repo-level installs merge to
// one entry, and removal never touches a user's own hooks. It must not be a
// substring of the sessionstatus handler's marker ("hook-handler") or vice
// versa.
const hookMarker = "record-hook"

// hookEvents are the Claude Code events the recorder listens on:
// UserPromptSubmit captures the transcript location and intent early,
// PostToolUse detects commits as they land, SessionEnd closes an unmanaged
// session's record.
var hookEvents = []string{"UserPromptSubmit", "PostToolUse", "SessionEnd"}

// excludeEntry keeps the installed settings file out of git status; it is
// written to the repository's shared info/exclude, never the working copy.
const excludeEntry = ".claude/settings.json"

// HookCommand returns the handler invocation installed into an agent's hook
// configuration. exe is the absolute wasa binary path.
func HookCommand(exe string) string {
	return exe + " " + hookMarker + " --tool claude"
}

type settingsHookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type settingsHookMatcher struct {
	Matcher string              `json:"matcher,omitempty"`
	Hooks   []settingsHookEntry `json:"hooks"`
}

// InstallClaudeHooks merges wasa's recording hook into dir/.claude/
// settings.json for every recorder event, where dir is a repository root or
// a worktree. It is additive and idempotent: user settings and hooks are
// preserved, and a re-install is a no-op once a recorder entry is present. A
// settings file that exists but is not valid JSON is left untouched and the
// error returned, so a hand-edited config is never clobbered. The settings
// file is also added to the repository's shared info/exclude (best-effort)
// so recording never shows up in git status.
func InstallClaudeHooks(dir, command string) error {
	path := settingsPath(dir)
	top, hooks, err := loadSettings(path)
	if err != nil {
		return err
	}
	changed := false
	for _, event := range hookEvents {
		if hasRecordHook(hooks[event]) {
			continue
		}
		hooks[event] = append(hooks[event], settingsHookMatcher{
			Hooks: []settingsHookEntry{{Type: "command", Command: command}},
		})
		changed = true
	}
	if changed {
		if err := writeSettings(path, top, hooks); err != nil {
			return err
		}
	}
	ensureExcluded(dir)
	return nil
}

// RemoveClaudeHooks removes wasa's recording entries from dir/.claude/
// settings.json, leaving every other setting and hook in place. The file is
// deleted when nothing else remains in it. A missing file is a no-op.
func RemoveClaudeHooks(dir string) error {
	path := settingsPath(dir)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	top, hooks, err := loadSettings(path)
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
	}
	if len(top) == 0 && len(hooks) == 0 {
		if err := os.Remove(path); err != nil {
			return err
		}
		_ = os.Remove(filepath.Dir(path))
		return nil
	}
	return writeSettings(path, top, hooks)
}

// HooksInstalled reports whether dir/.claude/settings.json carries wasa's
// recording hook.
func HooksInstalled(dir string) bool {
	_, hooks, err := loadSettings(settingsPath(dir))
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

func settingsPath(dir string) string {
	return filepath.Join(dir, ".claude", "settings.json")
}

// loadSettings parses a settings file into its top-level raw fields and its
// decoded hooks map. A missing or empty file yields empty maps.
func loadSettings(path string) (
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

// writeSettings re-encodes the settings with the given hooks map, atomically.
func writeSettings(
	path string,
	top map[string]json.RawMessage,
	hooks map[string][]settingsHookMatcher,
) error {
	rawHooks, err := json.Marshal(hooks)
	if err != nil {
		return err
	}
	top["hooks"] = rawHooks
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

func hasRecordHook(matchers []settingsHookMatcher) bool {
	for _, m := range matchers {
		if matcherIsRecordHook(m) {
			return true
		}
	}
	return false
}

func matcherIsRecordHook(m settingsHookMatcher) bool {
	for _, h := range m.Hooks {
		if strings.Contains(h.Command, hookMarker) {
			return true
		}
	}
	return false
}

// ensureExcluded appends the settings file to the repository's shared
// info/exclude when missing, so the installed hook configuration never
// dirties git status or blocks a clean worktree removal. Tracked files are
// unaffected by exclude; a repository that commits .claude/settings.json
// keeps full control of it. Best-effort: any failure is ignored, costing
// only an untracked-file line in git status.
func ensureExcluded(dir string) {
	commonDir, err := gitIn(
		dir, nil, "rev-parse", "--path-format=absolute", "--git-common-dir",
	)
	if err != nil || commonDir == "" {
		return
	}
	path := filepath.Join(commonDir, "info", "exclude")
	data, _ := os.ReadFile(path)
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(line) == excludeEntry {
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
	fmt.Fprintf(f, "%s%s\n", prefix, excludeEntry)
}
