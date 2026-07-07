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
	"time"

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

const checkpointsUsage = "usage: wasa checkpoints " +
	"[show <id> | explain <commit-ish> | prune --before <date>]"

const checkpointsHelp = `usage: wasa checkpoints [show <id> | explain <commit-ish> | prune --before <date>]

Read back the session record of the repository containing the current
directory. Without arguments, lists every recorded session: id, branch, when
it was last checkpointed and how many commits it produced. "show" prints one
session's intent and metadata and pages its transcript; <id> may be a session
id or a checkpoint ULID, and either may be a unique prefix. "explain" answers
"why does this commit exist?": it finds the checkpoint(s) that produced
<commit-ish> and prints that session's intent, meta, and transcript. "prune"
deletes every checkpoint recorded before <date> (YYYY-MM-DD or RFC3339),
locally only — push afterwards to prune a remote.

Read-only (apart from prune) and plain git underneath: it works on any clone
that has the record, which transfers with

  git fetch origin %q

Flags:
  -h, --help   show this help and exit

explain flags:
      --all             print every checkpoint referencing the commit, not just the newest
      --no-transcript   print intent and meta only, skip the transcript
`

func runCheckpoints(args []string) error {
	fs := newFlagSet("wasa checkpoints")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintf(os.Stdout, checkpointsHelp, record.FetchRefspec)
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
	case len(rest) >= 1 && rest[0] == "explain":
		return explainCheckpoint(repoPath, rest[1:])
	case len(rest) >= 1 && rest[0] == "prune":
		return pruneCheckpoints(repoPath, rest[1:])
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
			"no recorded sessions in %s\n(recording writes to %s/*; "+
				"on a clone, fetch it with: git fetch origin %q)\n",
			repoPath, record.RefPrefix, record.FetchRefspec,
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

func explainCheckpoint(repoPath string, args []string) error {
	fs := newFlagSet("wasa checkpoints explain")
	all := fs.Bool(
		"all", false,
		"print every checkpoint referencing the commit, not just the newest",
	)
	noTranscript := fs.Bool(
		"no-transcript", false,
		"print intent and meta only, skip the transcript",
	)
	flags, positional := splitFlags(args)
	if err := fs.Parse(flags); err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New(
			"usage: wasa checkpoints explain [--all] [--no-transcript] <commit-ish>",
		)
	}
	commitish := positional[0]

	matches, searched, err := record.Explain(repoPath, commitish, *all)
	if errors.Is(err, record.ErrNoRecord) {
		return fmt.Errorf(
			"recording has never run in %s (no %s; on a clone, fetch it with: "+
				"git fetch origin %q)",
			repoPath, record.RefPrefix, record.FetchRefspec,
		)
	}
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return fmt.Errorf(
			"no checkpoint references %s (searched %d checkpoint(s) on %s)",
			commitish, searched, record.RefPrefix,
		)
	}

	for i, m := range matches {
		if i > 0 {
			fmt.Fprintln(os.Stdout)
		}
		if err := printExplained(m, *noTranscript); err != nil {
			return err
		}
	}
	return nil
}

// splitFlags separates dash-prefixed flags from positional arguments so the
// commit-ish may appear before or after the flags. Both explain flags are
// booleans with no separate value, and a commit-ish never starts with a dash.
func splitFlags(args []string) (flags, positional []string) {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
		} else {
			positional = append(positional, a)
		}
	}
	return flags, positional
}

// printExplained prints one matching checkpoint: intent first, then a compact
// meta summary, then the transcript through the pager unless withoutTranscript.
func printExplained(m record.Match, withoutTranscript bool) error {
	fmt.Fprintf(os.Stdout, "session %s (checkpoint %s)\n\n",
		m.Meta.SessionID, m.CommitSHA)
	fmt.Fprintf(os.Stdout, "intent:\n%s\n\n", indent(m.Intent))

	when := m.Meta.FinishedAt
	if when.IsZero() {
		when = m.Meta.StartedAt
	}
	if when.IsZero() {
		when = m.When
	}
	agent := m.Meta.Agent
	if agent == "" {
		agent = "(unknown)"
	}
	fmt.Fprintf(os.Stdout,
		"meta:\n  session: %s\n  branch:  %s\n  agent:   %s\n  when:    %s\n\n",
		m.Meta.SessionID, m.Meta.Branch, agent,
		when.Local().Format("2006-01-02 15:04"),
	)

	if withoutTranscript {
		return nil
	}
	if len(m.Transcript) == 0 {
		fmt.Fprintln(os.Stdout, "transcript: (not captured)")
		return nil
	}
	fmt.Fprintln(os.Stdout, "transcript:")
	return page(m.Transcript)
}

func pruneCheckpoints(repoPath string, args []string) error {
	fs := newFlagSet("wasa checkpoints prune")
	before := fs.String(
		"before", "", "delete checkpoints recorded before this date "+
			"(YYYY-MM-DD or RFC3339)",
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *before == "" {
		return errors.New("usage: wasa checkpoints prune --before <date>")
	}
	cutoff, err := parseDate(*before)
	if err != nil {
		return err
	}
	n, err := record.Prune(repoPath, cutoff)
	if err != nil {
		return err
	}
	fmt.Fprintf(
		os.Stdout, "pruned %d checkpoint(s) recorded before %s (local only; "+
			"push to prune a remote)\n",
		n, cutoff.Local().Format("2006-01-02 15:04"),
	)
	return nil
}

// parseDate accepts a full RFC3339 timestamp or a plain YYYY-MM-DD, which is
// taken as local midnight.
func parseDate(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf(
		"invalid date %q: want YYYY-MM-DD or RFC3339", s,
	)
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
