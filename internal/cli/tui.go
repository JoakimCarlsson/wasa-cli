package cli

import (
	"io"
	"os"

	"github.com/mattn/go-isatty"

	"github.com/joakimcarlsson/wasa/internal/tui"
)

// runCockpit opens the registry, reconciles it and launches the Bubble Tea
// cockpit focused on the current repository's workspace. It is the bare-wasa
// entry point: running wasa with no subcommand inside a known repo.
func runCockpit() error {
	reg, current, err := openRegistry()
	if err != nil {
		return err
	}

	currentID := ""
	if current != nil {
		currentID = current.ID
	}
	return tui.Run(wasaHome(), reg, currentID)
}

// interactive reports whether w is a terminal the cockpit can take over. It
// guards the no-argument launch so non-interactive callers, including tests that
// pass a buffer, fall back to printing usage instead of starting the TUI.
func interactive(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && isatty.IsTerminal(f.Fd())
}
