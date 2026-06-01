package sessionstatus

import (
	"os"
	"path/filepath"
	"strings"
)

// Adapter is one hook-emitting agent's integration. Each supported agent is one
// Adapter and one file in this package. The three methods are the only
// tool-specific knowledge in wasa:
//
//   - Matches recognises the agent from the launch program token.
//   - MapEvent translates that agent's lifecycle event names into a Status.
//   - Install writes wasa's hook into the agent's own configuration so it calls
//     `wasa hook-handler --tool <name>` on those events. env is the session
//     environment, from which an adapter resolves its config directory (honouring
//     a profile's relocation of it); command is the handler invocation to install.
//     Install is additive and idempotent and must never clobber a user's config.
type Adapter interface {
	Name() string
	Matches(program string) bool
	MapEvent(event string) (Status, bool)
	Install(env []string, command string) error
}

// adapters is the registry of every hook-emitting agent wasa supports.
var adapters = []Adapter{
	claudeAdapter{},
	geminiAdapter{},
	codexAdapter{},
	opencodeAdapter{},
	copilotAdapter{},
	cursorAdapter{},
}

// All returns every registered adapter.
func All() []Adapter { return append([]Adapter(nil), adapters...) }

// For returns the adapter matching a launch program, or false when no
// hook-emitting agent recognises it — a shell or arbitrary program, whose status
// the cockpit derives from the pane heuristic instead.
func For(program string) (Adapter, bool) {
	for _, a := range adapters {
		if a.Matches(program) {
			return a, true
		}
	}
	return nil, false
}

// Lookup returns the adapter with the given name. The hook handler uses it: it
// is told its tool explicitly via --tool rather than guessing.
func Lookup(name string) (Adapter, bool) {
	for _, a := range adapters {
		if a.Name() == name {
			return a, true
		}
	}
	return nil, false
}

// toolName reduces a launch token ("/usr/bin/claude --resume") to its base
// executable name ("claude"). Shared by every adapter's Matches.
func toolName(program string) string {
	program = strings.TrimSpace(program)
	if program == "" {
		return ""
	}
	return filepath.Base(strings.Fields(program)[0])
}

// envValue returns the value of key in a KEY=VALUE environment slice, scanning
// from the end so a later entry wins, or "" when absent.
func envValue(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], prefix); ok {
			return v
		}
	}
	return ""
}

// configDir resolves an agent's configuration directory: the value of envKey in
// env when a profile relocated it, otherwise homeSub under the user's home
// directory (e.g. ".claude"). envKey may be "" for agents with no relocation
// variable.
func configDir(env []string, envKey, homeSub string) string {
	if envKey != "" {
		if v := envValue(env, envKey); v != "" {
			return v
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return homeSub
	}
	return filepath.Join(home, homeSub)
}

// atomicWrite writes data to path via a temp file in the same directory and a
// rename, creating parent directories, so a reader never sees a partial file.
func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".wasa-tmp-*")
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
	return os.Rename(tmpName, path)
}
