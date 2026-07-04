package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/mattn/go-isatty"

	"github.com/joakimcarlsson/wasa-cli/internal/record"
	"github.com/joakimcarlsson/wasa-cli/internal/worktree"
)

func init() {
	commands = append(commands, &Command{
		Name:    "checkpoints",
		Summary: "list or show recorded agent sessions for this repository",
		Run:     runCheckpoints,
	})
}

const checkpointsUsage = "usage: wasa checkpoints [show <session-id>]"

const checkpointsHelp = `usage: wasa checkpoints [show <session-id>]

Read back the session record of the repository containing the current
directory. Without arguments, lists every recorded session: id, branch, when
it was last checkpointed and how many commits it produced. "show" prints one
session's intent and metadata and pages its transcript. <session-id> may be
a unique prefix.

Read-only and plain git underneath: it works on any clone that has the
record, which transfers with

  git fetch origin %s:%s

Flags:
  -h, --help   show this help and exit
`

func runCheckpoints(args []string) error {
	fs := newFlagSet("wasa checkpoints")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintf(
				os.Stdout, checkpointsHelp, record.RefName, record.RefName,
			)
			return nil
		}
		return err
	}
	rest := fs.Args()

	repoPath, err := worktree.Toplevel(".")
	if err != nil {
		return err
	}

	switch {
	case len(rest) == 0:
		return listCheckpoints(repoPath)
	case len(rest) == 2 && rest[0] == "show":
		return showCheckpoint(repoPath, rest[1])
	default:
		return errors.New(checkpointsUsage)
	}
}

func listCheckpoints(repoPath string) error {
	entries, err := record.List(repoPath)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Fprintf(
			os.Stdout,
			"no recorded sessions in %s\n(recording writes to %s; "+
				"on a clone, fetch it with: git fetch origin %s:%s)\n",
			repoPath, record.RefName, record.RefName, record.RefName,
		)
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SESSION\tBRANCH\tWHEN\tCOMMITS\tSTATE")
	for _, e := range entries {
		state := "open"
		if !e.Meta.FinishedAt.IsZero() {
			state = "finished"
		}
		if e.Meta.Unmanaged {
			state += ", unmanaged"
		}
		fmt.Fprintf(
			w, "%s\t%s\t%s\t%d\t%s\n",
			e.Meta.SessionID,
			e.Meta.Branch,
			e.When.Local().Format("2006-01-02 15:04"),
			len(e.Meta.Commits),
			state,
		)
	}
	return w.Flush()
}

func showCheckpoint(repoPath, query string) error {
	e, intent, transcript, err := record.Find(repoPath, query)
	if err != nil {
		return err
	}

	meta, err := json.MarshalIndent(e.Meta, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "session %s (checkpoint %s)\n\n",
		e.Meta.SessionID, e.CommitSHA)
	fmt.Fprintf(os.Stdout, "intent:\n%s\n\n", indent(intent))
	fmt.Fprintf(os.Stdout, "meta:\n%s\n\n", indent(string(meta)))
	if len(transcript) == 0 {
		fmt.Fprintln(os.Stdout, "transcript: (not captured)")
		return nil
	}
	fmt.Fprintln(os.Stdout, "transcript:")
	return page(transcript)
}

func indent(s string) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return "  (empty)"
	}
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}

// page writes b through $PAGER (default less) when stdout is a terminal, so
// a long transcript stays navigable, and plainly otherwise so redirection
// and pipes keep working.
func page(b []byte) error {
	if !isatty.IsTerminal(os.Stdout.Fd()) {
		_, err := os.Stdout.Write(b)
		return err
	}
	pager := os.Getenv("PAGER")
	if pager == "" {
		pager = "less"
	}
	cmd := exec.Command("sh", "-c", pager)
	cmd.Stdin = strings.NewReader(string(b))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		_, werr := os.Stdout.Write(b)
		return werr
	}
	return nil
}
