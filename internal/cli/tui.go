package cli

import (
	"context"
	"io"
	"os"

	"github.com/mattn/go-isatty"

	"github.com/joakimcarlsson/wasa-cli/internal/config"
	"github.com/joakimcarlsson/wasa-cli/internal/link"
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

	stopLink := startLinkLoop()
	defer stopLink()

	return tui.Run(wasaHome(), reg, currentID, cfg)
}

// startLinkLoop dials out to the control plane for the cockpit's lifetime
// when the runner is linked. It is silent and best-effort: no credential, an
// unreadable file or an offline api never blocks or degrades the cockpit.
func startLinkLoop() (stop func()) {
	creds, ok, err := link.LoadCredentials(wasaHome())
	if err != nil || !ok {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	go link.Loop(ctx, creds, buildVersion)
	return cancel
}

// interactive reports whether w is a terminal the cockpit can take over. It
// guards the no-argument launch so non-interactive callers, including tests that
// pass a buffer, fall back to printing usage instead of starting the TUI.
func interactive(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && isatty.IsTerminal(f.Fd())
}
