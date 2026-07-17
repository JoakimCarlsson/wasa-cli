package agent

import (
	"slices"
	"strings"
	"testing"
)

// TestAgentsInvariants asserts the internal consistency guarantees the
// registry promises: every agent has a unique, non-empty Exe, a declared
// Autonomy always carries a non-empty flag, and ConfigDirAliases are never
// declared without a ConfigDirVar to alias.
func TestAgentsInvariants(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range Agents {
		if a.Exe == "" {
			t.Fatalf("agent with empty Exe: %+v", a)
		}
		if seen[a.Exe] {
			t.Fatalf("duplicate Exe %q in Agents", a.Exe)
		}
		seen[a.Exe] = true

		if a.Autonomy != nil && a.Autonomy.Flag == "" {
			t.Errorf("agent %q declares Autonomy with an empty Flag", a.Exe)
		}
		if a.ConfigDirVar == "" && len(a.ConfigDirAliases) > 0 {
			t.Errorf(
				"agent %q declares ConfigDirAliases %v without a ConfigDirVar",
				a.Exe, a.ConfigDirAliases,
			)
		}

		dirSeen := map[string]bool{}
		for _, d := range a.ProjectConfigDirs {
			switch {
			case d == "":
				t.Errorf("agent %q declares an empty ProjectConfigDir", a.Exe)
			case strings.ContainsAny(d, `/\`):
				t.Errorf(
					"agent %q ProjectConfigDir %q is not a single path segment",
					a.Exe, d,
				)
			case !strings.HasPrefix(d, "."):
				t.Errorf(
					"agent %q ProjectConfigDir %q is not dot-prefixed",
					a.Exe, d,
				)
			case dirSeen[d]:
				t.Errorf(
					"agent %q declares duplicate ProjectConfigDir %q", a.Exe, d,
				)
			}
			dirSeen[d] = true
		}
	}
}

// declaredGapReasons is the parity allowlist for a capability every
// currently-declared agent legitimately lacks: exe -> one-line rationale
// (kept here, not in agent.go's comments, so this test is the single place
// that must be touched — and so fails loudly — when a future agent leaves a
// capability empty without a matching entry).
var declaredConfigDirGapReasons = map[string]string{
	"cursor-agent": "cursor-agent's CLI reference documents no config-dir override env var (only CURSOR_API_KEY); its config always lives under ~/.cursor",
	"aider":        "aider resolves .aider.conf.yml from git root/cwd/home with no directory-override env var",
}

// declaredProjectConfigDirGapReasons is the parity allowlist for agents that
// keep no project-scoped config *directory*: exe -> one-line rationale. Same
// contract as declaredConfigDirGapReasons — an agent left with nil
// ProjectConfigDirs and no entry here fails CI rather than shipping a silently
// undeclared config surface.
var declaredProjectConfigDirGapReasons = map[string]string{
	"aider": "aider's project config is loose files (.aider.conf.yml, .aider.chat.history.md), not a directory",
}

// TestCapabilityGapsAreDeclaredExplicit is the parity test the agent-parity
// issue asks for: every declared agent must have each of {ConfigDirVar,
// Autonomy, RecorderTool} either implemented or named, with a reason, in this
// test's allowlist. An agent added to Agents with a capability left at its
// zero value and no matching allowlist entry fails CI instead of shipping a
// silently half-registered agent.
func TestCapabilityGapsAreDeclaredExplicit(t *testing.T) {
	for _, a := range Agents {
		if a.ConfigDirVar == "" {
			if _, ok := declaredConfigDirGapReasons[a.Exe]; !ok {
				t.Errorf(
					"agent %q has no ConfigDirVar and no declared reason in "+
						"declaredConfigDirGapReasons; either give it one or "+
						"add an explicit exception",
					a.Exe,
				)
			}
		}
		if a.Autonomy == nil {
			t.Errorf(
				"agent %q has no Autonomy flag declared; every currently "+
					"declared agent has one — if a future agent genuinely "+
					"lacks one, add an explicit exception here",
				a.Exe,
			)
		}
		if a.RecorderTool == "" {
			t.Errorf(
				"agent %q has no RecorderTool declared; every currently "+
					"declared agent has a recorder — if a future agent "+
					"genuinely lacks one, add an explicit exception here",
				a.Exe,
			)
		}
		if len(a.ProjectConfigDirs) == 0 {
			if _, ok := declaredProjectConfigDirGapReasons[a.Exe]; !ok {
				t.Errorf(
					"agent %q has no ProjectConfigDirs and no declared reason "+
						"in declaredProjectConfigDirGapReasons; either give it "+
						"one or add an explicit exception",
					a.Exe,
				)
			}
		}
	}
}

func TestExesPreservesOrder(t *testing.T) {
	got := Exes()
	if len(got) != len(Agents) {
		t.Fatalf("Exes() len = %d, want %d", len(got), len(Agents))
	}
	for i, a := range Agents {
		if got[i] != a.Exe {
			t.Fatalf("Exes()[%d] = %q, want %q", i, got[i], a.Exe)
		}
	}
}

func TestByExe(t *testing.T) {
	a, ok := ByExe("claude")
	if !ok || a.Exe != "claude" {
		t.Fatalf("ByExe(claude) = %+v, %v", a, ok)
	}
	if _, ok := ByExe("nonexistent"); ok {
		t.Fatal("ByExe(nonexistent) reported found; want not found")
	}
}

func TestByRecorderTool(t *testing.T) {
	a, ok := ByRecorderTool("cursor")
	if !ok || a.Exe != "cursor-agent" {
		t.Fatalf(
			"ByRecorderTool(cursor) = %+v, %v; want cursor-agent",
			a, ok,
		)
	}
	if a, ok := ByRecorderTool("aider"); !ok || a.Exe != "aider" {
		t.Fatalf("ByRecorderTool(aider) = %+v, %v; want aider", a, ok)
	}
	if _, ok := ByRecorderTool("nonexistent"); ok {
		t.Fatal("ByRecorderTool(nonexistent) reported found; want not found")
	}
}

func TestConfigDirVarAliasing(t *testing.T) {
	cases := []struct {
		program string
		want    string
		wantOK  bool
	}{
		{"claude", "CLAUDE_CONFIG_DIR", true},
		{"codex", "CODEX_HOME", true},
		{"gemini", "GEMINI_CONFIG_DIR", true},
		{"copilot", "GH_CONFIG_DIR", true},
		{"gh", "GH_CONFIG_DIR", true},
		{"cursor-agent", "", false},
		{"aider", "", false},
		{"bash", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := ConfigDirVar(tc.program)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf(
				"ConfigDirVar(%q) = (%q, %v), want (%q, %v)",
				tc.program, got, ok, tc.want, tc.wantOK,
			)
		}
	}
}

func TestProjectConfigDirs(t *testing.T) {
	cases := []struct {
		program string
		want    []string
		wantOK  bool
	}{
		{"claude", []string{".claude"}, true},
		{"codex", []string{".codex"}, true},
		{"gemini", []string{".gemini"}, true},
		{"copilot", []string{".github"}, true},
		{"cursor-agent", []string{".cursor"}, true},
		{"aider", nil, true},
		{"gh", nil, false},
		{"bash", nil, false},
		{"", nil, false},
	}
	for _, tc := range cases {
		got, ok := ProjectConfigDirs(tc.program)
		if ok != tc.wantOK || !slices.Equal(got, tc.want) {
			t.Errorf(
				"ProjectConfigDirs(%q) = (%v, %v), want (%v, %v)",
				tc.program, got, ok, tc.want, tc.wantOK,
			)
		}
	}
}

func TestAllProjectConfigDirs(t *testing.T) {
	got := AllProjectConfigDirs()
	want := []string{".claude", ".codex", ".cursor", ".gemini", ".github"}
	if !slices.Equal(got, want) {
		t.Fatalf("AllProjectConfigDirs() = %v, want %v", got, want)
	}
	if !slices.IsSorted(got) {
		t.Errorf("AllProjectConfigDirs() = %v, not sorted", got)
	}
}
