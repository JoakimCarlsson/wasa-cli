package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/joakimcarlsson/wasa-cli/internal/agent"
	"github.com/joakimcarlsson/wasa-cli/internal/registry"
)

// Support is one agent's MCP standing, as `wasa mcp list` reports it: the
// agent's executable name, whether wasa can hand it MCP servers, and the
// working-tree file it writes them into when it can.
type Support struct {
	Exe       string `json:"exe"`
	Supported bool   `json:"supported"`
	Path      string `json:"path,omitempty"`
}

// Agents returns every declared agent's MCP standing, in the canonical
// agent.Agents order. An agent with no MCP mechanism is listed as unsupported
// rather than omitted, so the absence is visible where the support is.
func Agents() []Support {
	out := make([]Support, 0, len(agent.Agents))
	for _, a := range agent.Agents {
		s := Support{Exe: a.Exe}
		if a.MCP != nil {
			s.Supported = true
			s.Path = a.MCP.Path
		}
		out = append(out, s)
	}
	return out
}

// Supported reports whether wasa can inject MCP servers into the agent whose
// base executable name is exe. An unknown program reports false.
func Supported(exe string) bool {
	_, ok := agent.MCPConfig(exe)
	return ok
}

// Install merges servers into the MCP configuration the agent whose base
// executable name is exe reads from dir, the session's working tree. It is a
// no-op — not an error — when servers is empty or exe declares no MCP
// mechanism, so a profile's declaration never blocks an agent that cannot use
// it.
//
// The merge is additive and name-keyed: entries already in the file are kept
// and a server wasa declares under the same name replaces it, so an agent
// configuration the repository tracks survives everything wasa does not name.
// A file that exists but is not valid JSON is left untouched and the error
// returned, so a hand-written config is never clobbered. The file is added to
// the repository's shared info/exclude (best-effort) so injection does not
// dirty git status.
func Install(dir, exe string, servers map[string]registry.MCPServer) error {
	cfg, ok := agent.MCPConfig(exe)
	if !ok || len(servers) == 0 {
		return nil
	}

	rel := filepath.FromSlash(cfg.Path)
	path := filepath.Join(dir, rel)
	top, err := loadJSON(path)
	if err != nil {
		return err
	}

	entries := map[string]json.RawMessage{}
	if raw, ok := top[cfg.Key]; ok {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return fmt.Errorf(
				"%s has malformed %s; leaving it untouched: %w",
				path, cfg.Key, err,
			)
		}
	}
	for name, server := range servers {
		raw, err := json.Marshal(server)
		if err != nil {
			return err
		}
		entries[name] = raw
	}

	merged, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	top[cfg.Key] = merged
	if err := writeJSON(path, top); err != nil {
		return err
	}
	ensureExcluded(dir, filepath.ToSlash(rel))
	return nil
}

// loadJSON reads a config file into its top-level raw fields. A missing or
// empty file yields an empty document, so install writes a fresh one.
func loadJSON(path string) (map[string]json.RawMessage, error) {
	top := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		return top, nil
	case err != nil:
		return nil, err
	case len(strings.TrimSpace(string(data))) == 0:
		return top, nil
	}
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf(
			"%s is not valid JSON; leaving it untouched: %w", path, err,
		)
	}
	return top, nil
}

// writeJSON re-encodes a config document atomically, creating its directory.
// The document is newline-terminated, so a config a user later opens or diffs
// looks like every other text file in their tree.
func writeJSON(path string, top map[string]json.RawMessage) error {
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
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

// ensureExcluded appends entry, a working-tree-relative path, to the
// repository's shared info/exclude when missing, so an injected MCP config
// never dirties git status or blocks a clean worktree removal. Tracked files
// are unaffected by exclude, so a repository that commits its MCP config keeps
// full control of it. Best-effort: any failure costs only an untracked-file
// line in git status.
func ensureExcluded(dir, entry string) {
	commonDir, err := gitIn(
		dir, "rev-parse", "--path-format=absolute", "--git-common-dir",
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

func gitIn(dir string, args ...string) (string, error) {
	out, err := exec.Command(
		"git", append([]string{"-C", dir}, args...)...,
	).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
