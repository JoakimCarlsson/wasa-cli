package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/joakimcarlsson/wasa/internal/worktree"
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
	return m.Remove(args[0])
}

func newManager() (*worktree.Manager, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	repo, err := repoToplevel(cwd)
	if err != nil {
		return nil, err
	}

	workspace := filepath.Base(repo)
	return worktree.New(repo, wasaHome(), workspace), nil
}

func repoToplevel(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository: %s", dir)
	}
	return strings.TrimSpace(string(out)), nil
}

func wasaHome() string {
	if h := os.Getenv("WASA_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".wasa"
	}
	return filepath.Join(home, ".wasa")
}
