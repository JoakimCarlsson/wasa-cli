package cli

import (
	"context"
	"io"
	"os"

	"github.com/mattn/go-isatty"

	"github.com/joakimcarlsson/wasa-api/pkg/protocol"

	"github.com/joakimcarlsson/wasa-cli/internal/config"
	"github.com/joakimcarlsson/wasa-cli/internal/link"
	"github.com/joakimcarlsson/wasa-cli/internal/registry"
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

	stopLink := startLinkLoop(reg)
	defer stopLink()

	return tui.Run(wasaHome(), reg, currentID, cfg)
}

// startLinkLoop dials out to the control plane for the cockpit's lifetime
// when the runner is linked. It is silent and best-effort: no credential, an
// unreadable file or an offline api never blocks or degrades the cockpit.
// Registry snapshots are built on the saving goroutine (the registry is not
// safe for concurrent use) and handed to the loop over a latest-wins channel.
func startLinkLoop(reg *registry.Registry) (stop func()) {
	creds, ok, err := link.LoadCredentials(wasaHome())
	if err != nil || !ok {
		return func() {}
	}

	states := make(chan protocol.State, 1)
	push := func() {
		st := snapshotState(reg)
		for {
			select {
			case states <- st:
				return
			default:
			}
			select {
			case <-states:
			default:
			}
		}
	}
	reg.SetOnChange(push)
	push()

	ctx, cancel := context.WithCancel(context.Background())
	go link.Loop(ctx, creds, buildVersion, states)
	return cancel
}

// snapshotState maps the registry into the wire snapshot the control plane
// consumes.
func snapshotState(reg *registry.Registry) protocol.State {
	workspaces := reg.ListWorkspaces()
	sessions := reg.ListSessions()
	st := protocol.State{
		Workspaces: make([]protocol.Workspace, 0, len(workspaces)),
		Sessions:   make([]protocol.Session, 0, len(sessions)),
	}
	for _, w := range workspaces {
		repo := w.RemoteURL
		if repo == "" {
			repo = w.RepoPath
		}
		st.Workspaces = append(st.Workspaces, protocol.Workspace{
			ID:   w.ID,
			Name: w.Name,
			Repo: repo,
		})
	}
	for _, s := range sessions {
		st.Sessions = append(st.Sessions, protocol.Session{
			ID:          s.ID,
			WorkspaceID: s.WorkspaceID,
			Branch:      s.Branch,
			Agent:       s.Program,
			Status:      s.Status,
			CreatedAt:   s.CreatedAt,
		})
	}
	return st
}

// interactive reports whether w is a terminal the cockpit can take over. It
// guards the no-argument launch so non-interactive callers, including tests that
// pass a buffer, fall back to printing usage instead of starting the TUI.
func interactive(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && isatty.IsTerminal(f.Fd())
}
