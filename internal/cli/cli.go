package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/joakimcarlsson/wasa-cli/internal/link/gitremote"
	"github.com/joakimcarlsson/wasa-cli/internal/record"
)

const programName = "wasa"

// Run parses top-level flags, dispatches to a subcommand and returns the
// process exit code. version is the build-stamped version string.
func Run(version string, args []string) int {
	return run(version, args, os.Stdout, os.Stderr)
}

// RunArgv is Run over a whole argv, dispatching on the name the binary was
// invoked under before it looks at the arguments.
//
// git resolves a remote helper by executable name, so a git-remote-wasa symlink
// pointing at wasa is the entire installation step for wasa:// remotes — no
// second binary to build, ship and keep in step.
func RunArgv(version string, argv []string) int {
	return runArgv(version, argv, os.Stdout, os.Stderr)
}

func runArgv(
	version string,
	argv []string,
	stdout, stderr io.Writer,
) int {
	if len(argv) == 0 {
		return run(version, nil, stdout, stderr)
	}
	if name := filepath.Base(argv[0]); name == gitremote.BinaryName {
		return runCommand(name, argv[1:], stderr)
	}
	return run(version, argv[1:], stdout, stderr)
}

func run(version string, args []string, stdout, stderr io.Writer) int {
	record.Version = version

	fs := flag.NewFlagSet(programName, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	var showVersion bool
	fs.BoolVar(
		&showVersion,
		"version",
		false,
		"print version information and exit",
	)

	switch err := fs.Parse(args); {
	case errors.Is(err, flag.ErrHelp):
		usage(stdout, version)
		return 0
	case err != nil:
		fmt.Fprintf(stderr, "%s: %v\n\n", programName, err)
		usage(stderr, version)
		return 2
	}

	if showVersion {
		fmt.Fprintln(stdout, versionLine(version))
		return 0
	}

	rest := fs.Args()
	if len(rest) >= 1 && rest[0] == "version" {
		return runVersionCmd(rest[1:], version, stdout, stderr)
	}
	if len(rest) == 0 {
		if interactive(stdout) {
			if err := runCockpit(); err != nil {
				fmt.Fprintf(stderr, "%s: %v\n", programName, err)
				return 1
			}
			return 0
		}
		usage(stdout, version)
		return 0
	}

	name := rest[0]
	if _, ok := lookup(name); !ok {
		fmt.Fprintf(stderr, "%s: unknown command %q\n\n", programName, name)
		usage(stderr, version)
		return 2
	}
	return runCommand(name, rest[1:], stderr)
}

// runCommand runs one registered subcommand and turns its error into an exit
// code. It is the whole of the process for a command wasa is invoked as rather
// than asked for — a git remote helper never reaches the flag parsing above.
//
// A subprocess wasa delegated to and that already explained its own failure
// reports a gitremote.ExitError: its status is passed through and nothing is
// printed, so the terminal shows git's account of what went wrong once.
func runCommand(name string, args []string, stderr io.Writer) int {
	cmd, ok := lookup(name)
	if !ok {
		fmt.Fprintf(stderr, "%s: unknown command %q\n", programName, name)
		return 2
	}
	err := cmd.Run(args)
	if err == nil {
		return 0
	}
	var exit *gitremote.ExitError
	if errors.As(err, &exit) && exit.Code > 0 {
		return exit.Code
	}
	fmt.Fprintf(stderr, "%s %s: %v\n", programName, name, err)
	return 1
}

func versionLine(version string) string {
	return fmt.Sprintf("%s version %s", programName, version)
}

// runVersionCmd handles the version subcommand. With --json it emits the build
// version and the structured-output contract version as a JSON object, the
// discovery surface a machine-readable consumer gates on; otherwise it prints
// the same human line as the --version flag.
func runVersionCmd(
	args []string,
	version string,
	stdout, stderr io.Writer,
) int {
	fs := newFlagSet(programName + " version")
	asJSON := jsonFlag(fs)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "%s version: %v\n", programName, err)
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "usage: %s version [--json]\n", programName)
		return 2
	}
	if *asJSON {
		if err := emitJSON(
			stdout, versionJSON{Version: version, Contract: outputContract},
		); err != nil {
			fmt.Fprintf(stderr, "%s version: %v\n", programName, err)
			return 1
		}
		return 0
	}
	fmt.Fprintln(stdout, versionLine(version))
	return 0
}

func usage(w io.Writer, version string) {
	fmt.Fprintf(
		w,
		"%s — a terminal cockpit for AI coding agents across repositories\n\n",
		programName,
	)
	fmt.Fprintf(w, "%s\n\n", versionLine(version))
	fmt.Fprintf(
		w,
		"Usage:\n  %s [flags] <command> [arguments]\n\n",
		programName,
	)
	fmt.Fprint(w, "Flags:\n")
	fmt.Fprint(w, "  --version    print version information and exit\n")
	fmt.Fprint(w, "  -h, --help   show this help and exit\n\n")
	fmt.Fprint(w, "Commands:\n")
	fmt.Fprintf(
		w, "  %-12s %s\n", "version",
		"print version information (--json for machine-readable output)",
	)
	if len(commands) == 0 {
		fmt.Fprint(
			w,
			"  (none yet — subcommands such as worktree, tmux, workspace and finish will plug in here)\n",
		)
		return
	}
	for _, c := range commands {
		if c.Hidden {
			continue
		}
		fmt.Fprintf(w, "  %-12s %s\n", c.Name, c.Summary)
	}
}
