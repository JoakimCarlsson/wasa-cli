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
	"[show <id> | explain <commit-ish> | search <query> | " +
	"import [--dry-run] [--from <dir>] | prune --before <date>]"

const checkpointsHelp = `usage: wasa checkpoints [show <id> | explain <commit-ish> | search <query> | import [--dry-run] [--from <dir>] | prune --before <date>]

Read back the session record of the repository containing the current
directory. Without arguments, lists every recorded session: id, branch, when
it was last checkpointed and how many commits it produced. "show" prints one
session's intent and metadata and pages its transcript; <id> may be a session
id or a checkpoint ULID, and either may be a unique prefix. "explain" answers
"why does this commit exist?": it finds the checkpoint(s) that produced
<commit-ish> and prints that session's intent, meta, and transcript. "search"
finds sessions by intent or transcript content and prints one block per match
so you can pick one for show/explain. "import" backfills the record from this
repo's pre-existing Claude Code transcripts (one checkpoint per past session,
redacted like a live one); it is idempotent — a re-run imports only what is
new. "prune" deletes every checkpoint recorded before <date> (YYYY-MM-DD or
RFC3339), locally only — push afterwards to prune a remote.

Read-only (apart from prune) and plain git underneath: it works on any clone
that has the record, which transfers with

  git fetch origin %q

Flags:
  -h, --help   show this help and exit

explain flags:
      --all             print every checkpoint referencing the commit, not just the newest
      --no-transcript   print intent and meta only, skip the transcript

search flags:
      --regex           match <query> as a regular expression, not a case-insensitive substring
      --intent-only     search intents only, skip transcripts
      --branch <name>   only search sessions on this branch
      --since <date>    only search checkpoints recorded on or after <date> (YYYY-MM-DD or RFC3339)
      --limit N         stop after N matching sessions (default 20)

import flags:
      --dry-run         list the sessions that would be imported, write nothing
      --from <dir>      import transcripts from <dir> instead of ~/.claude/projects/<slug>/
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
	case len(rest) >= 1 && rest[0] == "search":
		return searchCheckpoints(repoPath, rest[1:])
	case len(rest) >= 1 && rest[0] == "prune":
		return pruneCheckpoints(repoPath, rest[1:])
	case len(rest) >= 1 && rest[0] == "import":
		return importCheckpoints(repoPath, rest[1:])
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
		if e.Meta.Imported {
			state += ", imported"
		} else if e.Meta.Unmanaged {
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
	return page([]byte(record.RenderTranscript(transcript)))
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
	flags, positional := partitionArgs(args, nil)
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

func searchCheckpoints(repoPath string, args []string) error {
	fs := newFlagSet("wasa checkpoints search")
	regex := fs.Bool(
		"regex", false,
		"match query as a regular expression instead of a substring",
	)
	intentOnly := fs.Bool(
		"intent-only", false, "search intents only, skip transcripts",
	)
	branch := fs.String("branch", "", "only search sessions on this branch")
	since := fs.String(
		"since", "", "only search checkpoints recorded on or after this date",
	)
	limit := fs.Int("limit", 20, "stop after this many matching sessions")

	flags, positional := partitionArgs(
		args, map[string]bool{"branch": true, "since": true, "limit": true},
	)
	if err := fs.Parse(flags); err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New(
			"usage: wasa checkpoints search [--regex] [--intent-only] " +
				"[--branch <name>] [--since <date>] [--limit N] <query>",
		)
	}

	opts := record.SearchOpts{
		Query:      positional[0],
		Regex:      *regex,
		IntentOnly: *intentOnly,
		Branch:     *branch,
		Limit:      *limit,
	}
	if *since != "" {
		t, err := parseDate(*since)
		if err != nil {
			return err
		}
		opts.Since = t
	}

	hits, err := record.Search(repoPath, opts)
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
	if len(hits) == 0 {
		return fmt.Errorf("no recorded session matches %q", opts.Query)
	}

	color := isatty.IsTerminal(os.Stdout.Fd())
	for _, h := range hits {
		branch := h.Meta.Branch
		if branch == "" {
			branch = "(none)"
		}
		fmt.Fprintf(os.Stdout, "%s  %s  %s  %s\n",
			h.Meta.SessionID, branch,
			h.When.Local().Format("2006-01-02 15:04"), h.File,
		)
		text, hs, he := record.Snippet(h.LineText, h.Start, h.End, 100)
		fmt.Fprintf(os.Stdout, "  %s\n\n", highlight(text, hs, he, color))
	}
	return nil
}

// highlight wraps text[hs:he] in bold yellow when color is set, else returns
// text unchanged. A pipe or file gets a plain snippet.
func highlight(text string, hs, he int, color bool) string {
	if !color || hs < 0 || he > len(text) || hs >= he {
		return text
	}
	return text[:hs] + "\x1b[1;33m" + text[hs:he] + "\x1b[0m" + text[he:]
}

// partitionArgs separates flags (and the values of flags that take one) from
// positional arguments, so a positional may appear before or after the flags
// — Go's flag package stops at the first non-flag argument. takesValue holds
// the bare names of flags that consume the next argument as their value when
// it is not given inline with "=".
func partitionArgs(
	args []string, takesValue map[string]bool,
) (flags, positional []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue
		}
		if takesValue[name] && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
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
	return page([]byte(record.RenderTranscript(m.Transcript)))
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

func importCheckpoints(repoPath string, args []string) error {
	fs := newFlagSet("wasa checkpoints import")
	dryRun := fs.Bool(
		"dry-run", false, "list what would be imported, write nothing",
	)
	from := fs.String(
		"from", "", "import transcripts from this directory instead of the "+
			"default ~/.claude/projects/<slug>/",
	)
	flags, positional := partitionArgs(args, map[string]bool{"from": true})
	if err := fs.Parse(flags); err != nil {
		return err
	}
	if len(positional) != 0 {
		return errors.New(
			"usage: wasa checkpoints import [--dry-run] [--from <dir>]",
		)
	}

	res, err := record.Import(repoPath, *from, *dryRun)
	if err != nil {
		return err
	}

	for _, w := range res.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
	if *dryRun {
		for _, c := range res.Imported {
			when := c.Meta.StartedAt.Local().Format("2006-01-02 15:04")
			fmt.Fprintf(os.Stdout, "would import %s  %s\n", c.SessionID, when)
		}
		fmt.Fprintf(
			os.Stdout, "dry run: %d to import, %d already present, %d failed\n",
			len(res.Imported), res.Skipped, res.Failed,
		)
		return nil
	}
	fmt.Fprintf(
		os.Stdout, "imported %d, skipped %d (already present), failed %d\n",
		len(res.Imported), res.Skipped, res.Failed,
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
