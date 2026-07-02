package profile

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/joakimcarlsson/wasa-cli/internal/registry"
)

func TestConfigDirVar(t *testing.T) {
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
		t.Run(tc.program, func(t *testing.T) {
			got, ok := ConfigDirVar(tc.program)
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf(
					"ConfigDirVar(%q) = (%q, %v), want (%q, %v)",
					tc.program, got, ok, tc.want, tc.wantOK,
				)
			}
		})
	}
}

func TestParseEnvFile(t *testing.T) {
	data := []byte(
		"# a comment\n" +
			"\n" +
			"  \n" +
			"FOO=bar\n" +
			"export BAZ=qux\n" +
			"QUOTED=\"with spaces\"\n" +
			"SINGLE='single'\n" +
			"  SPACED  =  value  \n" +
			"NO_EQUALS_LINE\n" +
			"=novalue\n",
	)

	got := parseEnvFile(data)
	want := map[string]string{
		"FOO":    "bar",
		"BAZ":    "qux",
		"QUOTED": "with spaces",
		"SINGLE": "single",
		"SPACED": "value",
	}

	if len(got) != len(want) {
		t.Fatalf("parseEnvFile = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("parseEnvFile[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestResolveEmptyProfile(t *testing.T) {
	got, err := Resolve(registry.Profile{Name: "default"}, "claude")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Resolve empty profile = %v, want no entries", got)
	}
}

func TestResolveSortedAndInlineEnv(t *testing.T) {
	got, err := Resolve(registry.Profile{
		Env: map[string]string{"ZED": "1", "ABLE": "2"},
	}, "claude")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := []string{"ABLE=2", "ZED=1"}
	if !slices.Equal(got, want) {
		t.Fatalf("Resolve = %v, want %v (sorted by key)", got, want)
	}
}

func TestResolvePrecedence(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.env")
	second := filepath.Join(dir, "second.env")
	writeFile(t, first, "KEY=fromFirst\nONLY_FIRST=a\n")
	writeFile(t, second, "KEY=fromSecond\nONLY_SECOND=b\n")

	p := registry.Profile{
		Env:            map[string]string{"KEY": "fromEnv", "FROM_ENV": "c"},
		EnvFiles:       []string{first, second},
		AgentConfigDir: "/cfg",
	}

	got, err := Resolve(p, "claude")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	want := []string{
		"CLAUDE_CONFIG_DIR=/cfg",
		"FROM_ENV=c",
		"KEY=fromEnv",
		"ONLY_FIRST=a",
		"ONLY_SECOND=b",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Resolve = %v, want %v", got, want)
	}
}

func TestResolveConfigDirOverridesEnv(t *testing.T) {
	p := registry.Profile{
		Env:            map[string]string{"CLAUDE_CONFIG_DIR": "/from-env"},
		AgentConfigDir: "/from-profile",
	}
	got, err := Resolve(p, "claude")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := []string{"CLAUDE_CONFIG_DIR=/from-profile"}
	if !slices.Equal(got, want) {
		t.Fatalf("Resolve = %v, want %v (config dir wins)", got, want)
	}
}

func TestResolveConfigDirIgnoredForUnknownProgram(t *testing.T) {
	p := registry.Profile{AgentConfigDir: "/cfg"}
	got, err := Resolve(p, "bash")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf(
			"Resolve = %v, want no entries for a program with no config-dir var",
			got,
		)
	}
}

func TestResolveCopilotUsesGHConfigDir(t *testing.T) {
	p := registry.Profile{AgentConfigDir: "/gh-cfg"}
	got, err := Resolve(p, "copilot")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := []string{"GH_CONFIG_DIR=/gh-cfg"}
	if !slices.Equal(got, want) {
		t.Fatalf("Resolve = %v, want %v", got, want)
	}
}

func TestResolveMissingEnvFileErrors(t *testing.T) {
	p := registry.Profile{
		EnvFiles: []string{filepath.Join(t.TempDir(), "nope.env")},
	}
	if _, err := Resolve(p, "claude"); err == nil {
		t.Fatal("Resolve with a missing env file returned nil error")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
