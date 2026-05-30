package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/joakimcarlsson/wasa/internal/registry"
	"github.com/joakimcarlsson/wasa/internal/tmux"
)

func init() {
	commands = append(commands,
		&Command{
			Name:    "workspace",
			Summary: "list and resolve per-repository workspaces",
			Run:     runWorkspace,
		},
		&Command{
			Name:    "session",
			Summary: "list agent sessions",
			Run:     runSession,
		},
	)
}

const workspaceUsage = "usage: wasa workspace <list|current>"

func runWorkspace(args []string) error {
	if len(args) == 0 {
		return errors.New(workspaceUsage)
	}

	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return workspaceList(rest)
	case "current":
		return workspaceCurrent(rest)
	default:
		return fmt.Errorf(
			"unknown workspace subcommand %q\n%s",
			sub,
			workspaceUsage,
		)
	}
}

func workspaceList(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: wasa workspace list")
	}

	reg, _, err := openRegistry()
	if err != nil {
		return err
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, w := range reg.ListWorkspaces() {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", w.ID, w.Name, w.RepoPath)
	}
	return tw.Flush()
}

func workspaceCurrent(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: wasa workspace current")
	}

	_, current, err := openRegistry()
	if err != nil {
		return err
	}
	if current == nil {
		return errors.New("not a git repository")
	}
	fmt.Fprintln(os.Stdout, current.ID)
	return nil
}

const sessionUsage = "usage: wasa session <list>"

func runSession(args []string) error {
	if len(args) == 0 {
		return errors.New(sessionUsage)
	}

	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return sessionList(rest)
	default:
		return fmt.Errorf(
			"unknown session subcommand %q\n%s",
			sub,
			sessionUsage,
		)
	}
}

func sessionList(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: wasa session list")
	}

	reg, _, err := openRegistry()
	if err != nil {
		return err
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, s := range reg.ListSessions() {
		fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\n",
			s.ID,
			s.Title,
			s.Branch,
			s.Status,
		)
	}
	return tw.Flush()
}

// openRegistry loads the registry, reconciles persisted sessions against tmux,
// and silently auto-registers the working directory's repository when it is a
// not-yet-known git repository. It returns the registry and the current
// workspace, which is nil when the working directory is not inside a git
// repository. Any state change is persisted before returning.
func openRegistry() (*registry.Registry, *registry.Workspace, error) {
	reg, err := registry.Open(wasaHome())
	if err != nil {
		return nil, nil, err
	}

	client := tmux.New()
	changed := reg.Reconcile(client.Has)

	var current *registry.Workspace
	if repoPath, remoteURL, rerr := currentRepo(); rerr == nil {
		ws, created := reg.EnsureWorkspace(
			repoPath,
			remoteURL,
			filepath.Base(repoPath),
		)
		current = ws
		changed = changed || created
	}

	if changed {
		if err := reg.Save(); err != nil {
			return nil, nil, err
		}
	}
	return reg, current, nil
}
