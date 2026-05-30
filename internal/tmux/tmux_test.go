package tmux

import (
	"slices"
	"strings"
	"testing"
)

func TestSpawnArgs(t *testing.T) {
	cases := []struct {
		name    string
		session string
		dir     string
		program []string
		want    []string
	}{
		{
			name:    "explicit program",
			session: "wasa_demo",
			dir:     "/work/repo",
			program: []string{"claude", "--flag"},
			want: []string{
				"new-session", "-d", "-s", "wasa_demo",
				"-c", "/work/repo", "claude", "--flag",
			},
		},
		{
			name:    "default program when none given",
			session: "wasa_demo",
			dir:     ".",
			program: nil,
			want: []string{
				"new-session", "-d", "-s", "wasa_demo",
				"-c", ".", DefaultProgram,
			},
		},
		{
			name:    "empty dir omits -c",
			session: "s",
			dir:     "",
			program: []string{"bash"},
			want:    []string{"new-session", "-d", "-s", "s", "bash"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := spawnArgs(tc.session, tc.dir, tc.program)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("spawnArgs = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTargetArgs(t *testing.T) {
	cases := []struct {
		name string
		got  []string
		want []string
	}{
		{"attach", attachArgs("s"), []string{"attach-session", "-t", "s"}},
		{"has", hasArgs("s"), []string{"has-session", "-t", "s"}},
		{"kill", killArgs("s"), []string{"kill-session", "-t", "s"}},
		{
			"list",
			listArgs(),
			[]string{"list-sessions", "-F", "#{session_name}"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !slices.Equal(tc.got, tc.want) {
				t.Fatalf("%s args = %v, want %v", tc.name, tc.got, tc.want)
			}
		})
	}
}

func TestValidateName(t *testing.T) {
	cases := []struct {
		name    string
		session string
		wantErr bool
	}{
		{"plain", "wasa_demo", false},
		{"scheme-shaped", "wasa_a1b2_c3d4", false},
		{"empty", "", true},
		{"colon", "a:b", true},
		{"dot", "a.b", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateName(tc.session)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateName(%q) err = %v, wantErr %v",
					tc.session, err, tc.wantErr)
			}
		})
	}
}

func TestParseSessions(t *testing.T) {
	cases := []struct {
		name   string
		stdout string
		want   []string
	}{
		{"empty", "", nil},
		{"single trailing newline", "wasa_demo\n", []string{"wasa_demo"}},
		{
			"multiple",
			"a\nb\nc\n",
			[]string{"a", "b", "c"},
		},
		{
			"blank lines skipped",
			"a\n\n  \nb\n",
			[]string{"a", "b"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseSessions(tc.stdout)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("parseSessions = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMissingBinaryError(t *testing.T) {
	c := &Client{Bin: "wasa-no-such-tmux-binary"}

	if err := c.Spawn("wasa_demo", ".", "bash"); err == nil {
		t.Fatal("Spawn with missing binary returned nil error")
	} else if !strings.Contains(err.Error(), "tmux binary not found") {
		t.Fatalf("Spawn error = %v, want a clear not-found message", err)
	}

	if _, err := c.Has("wasa_demo"); err == nil {
		t.Fatal("Has with missing binary returned nil error")
	} else if !strings.Contains(err.Error(), "tmux binary not found") {
		t.Fatalf("Has error = %v, want a clear not-found message", err)
	}

	if _, err := c.List(); err == nil {
		t.Fatal("List with missing binary returned nil error")
	} else if !strings.Contains(err.Error(), "tmux binary not found") {
		t.Fatalf("List error = %v, want a clear not-found message", err)
	}

	if err := c.Kill("wasa_demo"); err == nil {
		t.Fatal("Kill with missing binary returned nil error")
	} else if !strings.Contains(err.Error(), "tmux binary not found") {
		t.Fatalf("Kill error = %v, want a clear not-found message", err)
	}
}
