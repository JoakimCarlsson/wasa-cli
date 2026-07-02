package cli

import (
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/joakimcarlsson/wasa-cli/internal/registry"
	"github.com/joakimcarlsson/wasa-cli/internal/worktree"
)

func init() {
	commands = append(commands, &Command{
		Name:    "worktree",
		Summary: "manage per-session git worktrees",
		Run:     runWorktree,
	})
}

const worktreeUsage = "usage: wasa worktree <add|list|remove> [arguments]"

func runWorktree(args []string) error {
	if len(args) == 0 {
		return errors.New(worktreeUsage)
	}

	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		return worktreeAdd(rest)
	case "list":
		return worktreeList(rest)
	case "remove":
		return worktreeRemove(rest)
	default:
		return fmt.Errorf(
			"unknown worktree subcommand %q\n%s",
			sub,
			worktreeUsage,
		)
	}
}

func worktreeAdd(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: wasa worktree add <branch>")
	}

	m, err := newManager()
	if err != nil {
		return err
	}

	path, err := m.Add(args[0])
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, path)
	return nil
}

func worktreeList(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: wasa worktree list")
	}

	m, err := newManager()
	if err != nil {
		return err
	}

	list, err := m.List()
	if err != nil {
		return err
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, w := range list {
		ref := w.Branch
		switch {
		case w.Bare:
			ref = "(bare)"
		case ref == "":
			ref = "(detached)"
		}
		fmt.Fprintf(tw, "%s\t%s\n", w.Path, ref)
	}
	return tw.Flush()
}

func worktreeRemove(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: wasa worktree remove <path|branch>")
	}

	m, err := newManager()
	if err != nil {
		return err
	}
	return m.Remove(args[0], false)
}

func newManager() (*worktree.Manager, error) {
	repoPath, remoteURL, err := currentRepo()
	if err != nil {
		return nil, err
	}

	id := registry.WorkspaceID(repoPath, remoteURL)
	return worktree.New(repoPath, wasaHome(), id), nil
}
