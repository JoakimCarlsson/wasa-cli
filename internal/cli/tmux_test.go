package cli

import (
	"strings"
	"testing"
)

func TestTmuxCommandRegistered(t *testing.T) {
	if _, ok := lookup("tmux"); !ok {
		t.Fatal("tmux command not registered")
	}
}

func TestRunTmuxNoArgs(t *testing.T) {
	err := runTmux(nil)
	if err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("runTmux(nil) err = %v, want usage error", err)
	}
}

func TestRunTmuxUnknownSubcommand(t *testing.T) {
	err := runTmux([]string{"bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown tmux subcommand") {
		t.Fatalf("runTmux err = %v, want unknown-subcommand error", err)
	}
}

func TestTmuxSpawnRequiresName(t *testing.T) {
	err := tmuxSpawn([]string{"--dir", "."})
	if err == nil || !strings.Contains(err.Error(), "--name") {
		t.Fatalf("tmuxSpawn without --name err = %v, want name usage", err)
	}
}

func TestSessionAndWorkspaceCommandsRegistered(t *testing.T) {
	for _, name := range []string{"session", "workspace"} {
		if _, ok := lookup(name); !ok {
			t.Fatalf("%s command not registered", name)
		}
	}
}

func TestRunSessionUnknownSubcommand(t *testing.T) {
	err := runSession([]string{"bogus"})
	if err == nil ||
		!strings.Contains(err.Error(), "unknown session subcommand") {
		t.Fatalf("runSession err = %v, want unknown-subcommand error", err)
	}
}
