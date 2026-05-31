//go:build windows

package cli

import (
	"errors"

	conpty "github.com/joakimcarlsson/wasa/internal/backend/windows"
)

// On Windows the native session backend (internal/conpty) needs wasa to run two
// background roles of itself: a long-lived session daemon that owns the
// pseudo-consoles, and a per-attach relay that bridges the terminal to a
// session. Both are wired here as hidden subcommands the backend invokes via
// os.Executable; users never type them.
func init() {
	commands = append(commands,
		&Command{
			Name:    conpty.DaemonSubcommand,
			Summary: "run the native Windows session daemon (internal)",
			Hidden:  true,
			Run:     runDaemon,
		},
		&Command{
			Name:    conpty.AttachSubcommand,
			Summary: "relay the terminal to a native Windows session (internal)",
			Hidden:  true,
			Run:     runAttach,
		},
	)
}

func runDaemon(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: wasa " + conpty.DaemonSubcommand)
	}
	return conpty.RunDaemon()
}

func runAttach(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: wasa " + conpty.AttachSubcommand + " <name>")
	}
	return conpty.RunAttach(args[0])
}
