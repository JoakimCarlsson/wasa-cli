package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/joakimcarlsson/wasa-cli/internal/record"
)

const programName = "wasa"

// Run parses top-level flags, dispatches to a subcommand and returns the
// process exit code. version is the build-stamped version string.
func Run(version string, args []string) int {
	return run(version, args, os.Stdout, os.Stderr)
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
