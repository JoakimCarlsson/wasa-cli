//go:build !windows

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
		env     []string
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
		{
			name:    "env injected as -e before -c and program",
			session: "wasa_demo",
			dir:     "/work/repo",
			env:     []string{"FOO=bar", "CLAUDE_CONFIG_DIR=/cfg"},
			program: []string{"claude"},
			want: []string{
				"new-session", "-d", "-s", "wasa_demo",
				"-e", "FOO=bar", "-e", "CLAUDE_CONFIG_DIR=/cfg",
				"-c", "/work/repo", "claude",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := spawnArgs(tc.session, tc.dir, tc.env, tc.program)
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
			"capture",
			captureArgs("s"),
			[]string{"capture-pane", "-e", "-p", "-t", "s"},
		},
		{
			"list",
			listArgs(),
			[]string{"list-sessions", "-F", "#{session_name}"},
		},
		{
			"remain-on-exit",
			remainOnExitArgs("s"),
			[]string{"set-option", "-t", "s", "-w", "remain-on-exit", "on"},
		},
		{
			"pane-exit",
			paneExitArgs("s"),
			[]string{
				"list-panes", "-t", "s",
				"-F", "#{pane_dead} #{pane_dead_status}",
			},
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

func TestAttachCmd(t *testing.T) {
	c := &Client{Bin: "my-tmux"}

	cmd, err := c.AttachCmd("wasa_demo")
	if err != nil {
		t.Fatalf("AttachCmd: %v", err)
	}
	if cmd.Path != "my-tmux" && !strings.HasSuffix(cmd.Path, "my-tmux") {
		t.Fatalf("AttachCmd path = %q, want my-tmux", cmd.Path)
	}
	want := []string{"my-tmux", "attach-session", "-t", "wasa_demo"}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("AttachCmd args = %v, want %v", cmd.Args, want)
	}
	if cmd.Stdin != nil || cmd.Stdout != nil || cmd.Stderr != nil {
		t.Fatal(
			"AttachCmd wired standard streams; tea.ExecProcess must own them",
		)
	}
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "TMUX=") {
			t.Fatal("AttachCmd left $TMUX set; nested attach would be refused")
		}
	}
}

func TestEnvWithout(t *testing.T) {
	in := []string{"PATH=/bin", "TMUX=/tmp/sock,1,0", "HOME=/root"}
	got := envWithout(in, "TMUX")
	want := []string{"PATH=/bin", "HOME=/root"}
	if !slices.Equal(got, want) {
		t.Fatalf("envWithout = %v, want %v", got, want)
	}
}

func TestAttachCmdRejectsBadName(t *testing.T) {
	if _, err := New().AttachCmd("a:b"); err == nil {
		t.Fatal("AttachCmd accepted an unaddressable name")
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

func TestParsePaneExit(t *testing.T) {
	ptr := func(i int) *int { return &i }
	cases := []struct {
		name      string
		stdout    string
		wantAlive bool
		wantCode  *int
	}{
		{"live pane", "0 \n", true, nil},
		{"dead clean exit", "1 0\n", false, ptr(0)},
		{"dead failure", "1 3\n", false, ptr(3)},
		{"dead on signal, no status", "1 \n", false, nil},
		{"empty output", "", false, nil},
		{"first pane wins", "1 2\n0 \n", false, ptr(2)},
		{"blank lines skipped", "\n  \n1 5\n", false, ptr(5)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			alive, code := parsePaneExit(tc.stdout)
			if alive != tc.wantAlive {
				t.Fatalf("alive = %v, want %v", alive, tc.wantAlive)
			}
			switch {
			case tc.wantCode == nil && code != nil:
				t.Fatalf("code = %d, want nil", *code)
			case tc.wantCode != nil && code == nil:
				t.Fatalf("code = nil, want %d", *tc.wantCode)
			case tc.wantCode != nil && *code != *tc.wantCode:
				t.Fatalf("code = %d, want %d", *code, *tc.wantCode)
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
