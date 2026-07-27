//go:build !windows

package gitremote

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/joakimcarlsson/wasa-cli/internal/link/repotoken"
)

func TestActionFileRoundTrip(t *testing.T) {
	f, err := NewActionFile()
	if err != nil {
		t.Fatalf("NewActionFile: %v", err)
	}
	t.Cleanup(func() { _ = f.Remove() })

	if err := f.Write(repotoken.Push); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := ReadAction(f.Path())
	if err != nil {
		t.Fatalf("ReadAction: %v", err)
	}
	if got != repotoken.Push {
		t.Errorf("action = %q, want %q", got, repotoken.Push)
	}

	info, err := os.Stat(f.Path())
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %v, want 0600", perm)
	}
}

func TestActionFileRemoveTakesTheDirectory(t *testing.T) {
	f, err := NewActionFile()
	if err != nil {
		t.Fatalf("NewActionFile: %v", err)
	}
	if err := f.Write(repotoken.Pull); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(f.Path())); !os.IsNotExist(err) {
		t.Errorf("directory survived Remove: %v", err)
	}
}

func TestReadActionRefusesWhatItCannotTrust(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "absent")
	if _, err := ReadAction(missing); err == nil {
		t.Error("ReadAction on a missing file: want an error")
	}

	garbage := filepath.Join(dir, "garbage")
	if err := os.WriteFile(garbage, []byte("shove"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := ReadAction(garbage); err == nil {
		t.Error("ReadAction on a bad direction: want an error")
	}
}

func TestDirectionOf(t *testing.T) {
	tests := []struct {
		line   string
		action repotoken.Action
		known  bool
	}{
		{"list\n", repotoken.Pull, true},
		{"list for-push\n", repotoken.Push, true},
		{"fetch 0000 refs/wasa/checkpoints/x\n", repotoken.Pull, true},
		{"get https://x /tmp/y\n", repotoken.Pull, true},
		{"push refs/wasa/a:refs/wasa/a\n", repotoken.Push, true},
		{"stateless-connect git-upload-pack\n", repotoken.Pull, true},
		{"stateless-connect git-receive-pack\n", repotoken.Push, true},
		{"connect git-receive-pack\n", repotoken.Push, true},
		{"stateless-connect git-something-new\n", "", false},
		{"capabilities\n", "", false},
		{"option verbosity 1\n", "", false},
		{"\n", "", false},
	}
	for _, tt := range tests {
		action, known := directionOf(tt.line)
		if known != tt.known || action != tt.action {
			t.Errorf(
				"directionOf(%q) = (%q, %v), want (%q, %v)",
				tt.line, action, known, tt.action, tt.known,
			)
		}
	}
}
