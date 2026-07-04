package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/joakimcarlsson/wasa-cli/internal/record"
	"github.com/joakimcarlsson/wasa-cli/internal/worktree"
)

func init() {
	commands = append(commands, &Command{
		Name:    "record",
		Summary: "enable, disable or inspect repo-level session recording",
		Run:     runRecord,
	})
}

const recordUsage = "usage: wasa record <enable|disable|status>"

const recordHelp = `usage: wasa record <enable|disable|status>

Repo-level session recording. "enable" installs persistent hook
configuration (Claude Code: .claude/settings.json) in the repository
containing the current directory, so ANY agent session run in it — including
sessions started directly, with no wasa session around them — is recorded as
checkpoints on the ` + record.RefName + ` ref. "disable" removes the hooks;
"status" reports the current state.

Recorded transcripts are redacted for common secret formats (API keys,
tokens, credentials) before they enter the repository. Redaction is
best-effort: it catches the common token shapes, not every possible secret.

The settings file is kept out of git status via the repository's
.git/info/exclude. Recording writes only to ` + record.RefName + `; branches,
index and working copy are never touched. See "wasa checkpoints" to read the
record back.

Flags:
  -h, --help   show this help and exit
`

func runRecord(args []string) error {
	fs := newFlagSet("wasa record")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, recordHelp)
			return nil
		}
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return errors.New(recordUsage)
	}

	repoPath, err := worktree.Toplevel(".")
	if err != nil {
		return err
	}

	switch rest[0] {
	case "enable":
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if err := record.InstallClaudeHooks(
			repoPath, record.HookCommand(exe),
		); err != nil {
			return err
		}
		fmt.Fprintf(
			os.Stdout,
			"recording enabled for %s\nagent sessions run in this repository "+
				"now record to %s (transcripts redacted best-effort)\n",
			repoPath, record.RefName,
		)
	case "disable":
		if err := record.RemoveClaudeHooks(repoPath); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "recording disabled for %s\n", repoPath)
	case "status":
		state := "disabled"
		if record.HooksInstalled(repoPath) {
			state = "enabled"
		}
		fmt.Fprintf(os.Stdout, "recording %s for %s\n", state, repoPath)
	default:
		return errors.New(recordUsage)
	}
	return nil
}
