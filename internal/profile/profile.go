package profile

import (
	"bufio"
	"bytes"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/joakimcarlsson/wasa-cli/internal/registry"
)

// configDirVars maps a program name to the environment variable that overrides
// its config/home directory. It is the single place to teach wasa a new
// program's config-dir convention.
var configDirVars = map[string]string{
	"claude":  "CLAUDE_CONFIG_DIR",
	"copilot": "GH_CONFIG_DIR",
	"gh":      "GH_CONFIG_DIR",
}

// ConfigDirVar returns the config-dir environment variable for program and
// whether one is known. A program with no known convention reports false, and
// its sessions receive no config-dir override.
func ConfigDirVar(program string) (string, bool) {
	v, ok := configDirVars[program]
	return v, ok
}

// Resolve computes the environment to inject into a session launched for
// program under p, as KEY=VALUE entries sorted by key for a deterministic argv.
//
// Precedence, from lowest to highest: env files are loaded first in the order
// listed, each overriding the previous; the inline Env map then overrides any
// value from a file; finally, when AgentConfigDir is set and program has a
// known config-dir variable, that variable is set to AgentConfigDir and
// overrides anything above it, so the per-repository account always wins.
func Resolve(p registry.Profile, program string) ([]string, error) {
	merged := map[string]string{}

	for _, path := range p.EnvFiles {
		vars, err := loadEnvFile(path)
		if err != nil {
			return nil, err
		}
		maps.Copy(merged, vars)
	}

	maps.Copy(merged, p.Env)

	if p.AgentConfigDir != "" {
		if v, ok := ConfigDirVar(program); ok {
			merged[v] = p.AgentConfigDir
		}
	}

	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+merged[k])
	}
	return out, nil
}

func loadEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read env file %q: %w", path, err)
	}
	return parseEnvFile(data), nil
}

// parseEnvFile parses standard KEY=VALUE .env lines. Blank lines and lines whose
// first non-space character is '#' are ignored, an optional leading "export " is
// dropped, and a single layer of matching single or double quotes is stripped
// from the value. A line without '=' is skipped.
func parseEnvFile(data []byte) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = unquote(strings.TrimSpace(value))
	}
	return out
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') ||
			(s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
