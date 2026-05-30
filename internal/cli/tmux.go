package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/joakimcarlsson/wasa/internal/tmux"
)

func init() {
	commands = append(commands, &Command{
		Name:    "tmux",
		Summary: "spawn and attach to tmux sessions",
		Run:     runTmux,
	})
}

const tmuxUsage = "usage: wasa tmux <spawn|attach|has|list|kill> [arguments]"

func runTmux(args []string) error {
	if len(args) == 0 {
		return errors.New(tmuxUsage)
	}

	sub, rest := args[0], args[1:]
	switch sub {
	case "spawn":
		return tmuxSpawn(rest)
	case "attach":
		return tmuxAttach(rest)
	case "has":
		return tmuxHas(rest)
	case "list":
		return tmuxList(rest)
	case "kill":
		return tmuxKill(rest)
	default:
		return fmt.Errorf("unknown tmux subcommand %q\n%s", sub, tmuxUsage)
	}
}

func tmuxSpawn(args []string) error {
	fs := newFlagSet("wasa tmux spawn")
	var name, dir string
	fs.StringVar(&name, "name", "", "session name (required)")
	fs.StringVar(&dir, "dir", ".", "working directory for the session")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if name == "" {
		return errors.New(
			"usage: wasa tmux spawn --name <name> [--dir <dir>] " +
				"[-- <program...>]",
		)
	}

	if err := tmux.New().Spawn(name, dir, fs.Args()...); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, name)
	return nil
}

func tmuxAttach(args []string) error {
	name, err := nameFlag("wasa tmux attach", args)
	if err != nil {
		return err
	}
	return tmux.New().Attach(name)
}

func tmuxHas(args []string) error {
	name, err := nameFlag("wasa tmux has", args)
	if err != nil {
		return err
	}

	ok, err := tmux.New().Has(name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("session %q does not exist", name)
	}
	fmt.Fprintln(os.Stdout, "exists")
	return nil
}

func tmuxList(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: wasa tmux list")
	}

	names, err := tmux.New().List()
	if err != nil {
		return err
	}
	for _, n := range names {
		fmt.Fprintln(os.Stdout, n)
	}
	return nil
}

func tmuxKill(args []string) error {
	name, err := nameFlag("wasa tmux kill", args)
	if err != nil {
		return err
	}
	return tmux.New().Kill(name)
}

func nameFlag(usage string, args []string) (string, error) {
	fs := newFlagSet(usage)
	var name string
	fs.StringVar(&name, "name", "", "session name (required)")
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if name == "" {
		return "", fmt.Errorf("usage: %s --name <name>", usage)
	}
	return name, nil
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return fs
}
