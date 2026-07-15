package agent

import "testing"

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
	if _, ok := ByRecorderTool("aider"); ok {
		t.Fatal("ByRecorderTool(aider) reported found; want not found (no recorder)")
	}
}

func TestConfigDirVarAliasing(t *testing.T) {
	cases := []struct {
		program string
		want    string
		wantOK  bool
	}{
		{"claude", "CLAUDE_CONFIG_DIR", true},
		{"copilot", "GH_CONFIG_DIR", true},
		{"gh", "GH_CONFIG_DIR", true},
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
