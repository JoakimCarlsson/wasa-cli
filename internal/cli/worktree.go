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
	branch, force, err := parseWorktreeAddArgs(args)
	if err != nil {
		return err
	}

	m, err := newManager()
	if err != nil {
		return err
	}

	path, err := m.Add(branch)
	if err != nil {
		var exists *worktree.ErrWorktreeExists
		if !errors.As(err, &exists) {
			return err
		}
		if !force {
			return fmt.Errorf(
				"%w\nrun `wasa worktree remove %s --force` or "+
					"`wasa worktree add %s --force` to replace it",
				exists, exists.Path, branch,
			)
		}
		if err := m.Remove(exists.Path, true); err != nil {
			return fmt.Errorf("clear existing worktree: %w", err)
		}
		path, err = m.Add(branch)
		if err != nil {
			return err
		}
	}
	fmt.Fprintln(os.Stdout, path)
	return nil
}

// parseWorktreeAddArgs splits `wasa worktree add <branch> [--force]` into the
// branch and whether an existing worktree at that branch's path should be
// cleared and replaced rather than reported as an error.
func parseWorktreeAddArgs(
	args []string,
) (branch string, force bool, err error) {
	const usage = "usage: wasa worktree add <branch> [--force]"
	var rest []string
	for _, a := range args {
		if a == "--force" {
			force = true
			continue
		}
		rest = append(rest, a)
	}
	if len(rest) != 1 {
		return "", false, errors.New(usage)
	}
	return rest[0], force, nil
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
