// Package cli implements the top-level wasa command-line interface: flag
// parsing, usage output and dispatch to subcommands.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

const programName = "wasa"

// Run parses top-level flags, dispatches to a subcommand and returns the
// process exit code. version is the build-stamped version string.
func Run(version string, args []string) int {
	return run(version, args, os.Stdout, os.Stderr)
}

func run(version string, args []string, stdout, stderr io.Writer) int {
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
	if len(rest) == 0 {
		usage(stdout, version)
		return 0
	}

	name := rest[0]
	cmd, ok := lookup(name)
	if !ok {
		fmt.Fprintf(stderr, "%s: unknown command %q\n\n", programName, name)
		usage(stderr, version)
		return 2
	}

	if err := cmd.Run(rest[1:]); err != nil {
		fmt.Fprintf(stderr, "%s %s: %v\n", programName, name, err)
		return 1
	}
	return 0
}

func versionLine(version string) string {
	return fmt.Sprintf("%s version %s", programName, version)
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
	if len(commands) == 0 {
		fmt.Fprint(
			w,
			"  (none yet — subcommands such as worktree, tmux, workspace and finish will plug in here)\n",
		)
		return
	}
	for _, c := range commands {
		fmt.Fprintf(w, "  %-12s %s\n", c.Name, c.Summary)
	}
}
