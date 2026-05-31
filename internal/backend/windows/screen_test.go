//go:build windows

package conpty

import "testing"

func TestScreenBasicText(t *testing.T) {
	s := newScreen(20, 5)
	s.Write([]byte("hello world"))
	if got := s.Snapshot(); got != "hello world" {
		t.Fatalf("Snapshot = %q, want %q", got, "hello world")
	}
}

func TestScreenNewlineAndCarriageReturn(t *testing.T) {
	s := newScreen(20, 5)
	s.Write([]byte("line one\r\nline two"))
	want := "line one\nline two"
	if got := s.Snapshot(); got != want {
		t.Fatalf("Snapshot = %q, want %q", got, want)
	}
}

func TestScreenCursorPositionAndOverwrite(t *testing.T) {
	s := newScreen(20, 5)
	s.Write([]byte("abcdef"))
	s.Write([]byte("\x1b[1;1H"))
	s.Write([]byte("XYZ"))
	if got := s.Snapshot(); got != "XYZdef" {
		t.Fatalf("Snapshot = %q, want %q", got, "XYZdef")
	}
}

func TestScreenEraseDisplay(t *testing.T) {
	s := newScreen(20, 5)
	s.Write([]byte("keep\r\ngone"))
	s.Write([]byte("\x1b[2J\x1b[1;1H"))
	s.Write([]byte("fresh"))
	if got := s.Snapshot(); got != "fresh" {
		t.Fatalf("Snapshot = %q, want %q", got, "fresh")
	}
}

func TestScreenSGRIgnored(t *testing.T) {
	s := newScreen(20, 5)
	s.Write([]byte("\x1b[31mred\x1b[0m text"))
	if got := s.Snapshot(); got != "red text" {
		t.Fatalf("Snapshot = %q, want %q", got, "red text")
	}
}

func TestScreenScroll(t *testing.T) {
	s := newScreen(10, 2)
	s.Write([]byte("a\r\nb\r\nc"))
	if got := s.Snapshot(); got != "b\nc" {
		t.Fatalf("Snapshot = %q, want %q", got, "b\nc")
	}
}

func TestScreenAltScreenClears(t *testing.T) {
	s := newScreen(20, 5)
	s.Write([]byte("primary"))
	s.Write([]byte("\x1b[?1049h"))
	if got := s.Snapshot(); got != "" {
		t.Fatalf("Snapshot after alt-screen = %q, want empty", got)
	}
}

func TestMergeEnvOverridesCaseInsensitive(t *testing.T) {
	merged := mergeEnv([]string{"Path=a", "FOO=1"}, []string{"PATH=b", "BAR=2"})
	got := map[string]string{}
	for _, e := range merged {
		for i := range len(e) {
			if e[i] == '=' {
				got[e[:i]] = e[i+1:]
				break
			}
		}
	}
	if got["PATH"] != "b" {
		t.Fatalf("PATH override = %q, want b (entry: %v)", got["PATH"], merged)
	}
	if got["BAR"] != "2" || got["FOO"] != "1" {
		t.Fatalf("merged env wrong: %v", merged)
	}
}
