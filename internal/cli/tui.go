package cli

import (
	"io"
	"os"

	"github.com/mattn/go-isatty"

	"github.com/joakimcarlsson/wasa-cli/internal/config"
	"github.com/joakimcarlsson/wasa-cli/internal/tui"
)

// runCockpit opens the registry, reconciles it and launches the Bubble Tea
// cockpit. It is the bare-wasa entry point: run inside a known repo it focuses
// that repository's workspace; run outside any git repo it opens the
// all-workspaces view, seeded with every registered workspace as a tab (and the
// empty-state banner when none exist) rather than erroring.
func runCockpit() error {
	reg, current, err := openRegistry()
	if err != nil {
		return err
	}

	cfg, err := config.Load(wasaHome())
	if err != nil {
		return err
	}

	currentID := ""
	if current != nil {
		currentID = current.ID
	}
	return tui.Run(wasaHome(), reg, currentID, cfg)
}

// interactive reports whether w is a terminal the cockpit can take over. It
// guards the no-argument launch so non-interactive callers, including tests that
// pass a buffer, fall back to printing usage instead of starting the TUI.
func interactive(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && isatty.IsTerminal(f.Fd())
}
