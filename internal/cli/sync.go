package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/joakimcarlsson/wasa-cli/internal/record"
	"github.com/joakimcarlsson/wasa-cli/internal/worktree"
)

func init() {
	commands = append(commands,
		&Command{
			Name:    "push",
			Summary: "push the local wasa record (refs/wasa/*) to a remote",
			Run:     runPush,
		},
		&Command{
			Name:    "pull",
			Summary: "fetch the wasa record (refs/wasa/*) from a remote",
			Run:     runPull,
		},
	)
}

const (
	pushUsage = "usage: wasa push [remote]"
	pullUsage = "usage: wasa pull [remote]"
)

// runPush pushes every local refs/wasa/* ref to remote (default origin) and
// reports what was sent. Because each checkpoint/review is its own uniquely
// named ref, this is a set union across runners, never an overwrite: two
// runners pushing against the same origin both survive intact.
func runPush(args []string) error {
	remote, err := syncArgs(pushUsage, args)
	if err != nil {
		return err
	}
	repoPath, err := worktree.Toplevel(".")
	if err != nil {
		return err
	}
	result, err := record.PushAll(repoPath, remote)
	if err != nil {
		if len(result.Refs) > 0 {
			printSyncSummary(os.Stdout, "sent", result.Refs)
		}
		return err
	}
	printSyncSummary(os.Stdout, "sent", result.Refs)
	return nil
}

// runPull fetches the whole refs/wasa/* namespace from remote (default
// origin) and integrates it locally, reporting what arrived. On a fresh
// clone this populates the entire record in one call.
func runPull(args []string) error {
	remote, err := syncArgs(pullUsage, args)
	if err != nil {
		return err
	}
	repoPath, err := worktree.Toplevel(".")
	if err != nil {
		return err
	}
	result, err := record.PullAll(repoPath, remote)
	if err != nil {
		return err
	}
	printSyncSummary(os.Stdout, "received", result.Refs)
	return nil
}

// syncArgs parses the optional remote argument shared by push and pull,
// defaulting to origin.
func syncArgs(usage string, args []string) (remote string, err error) {
	fs := newFlagSet(usage)
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	switch rest := fs.Args(); len(rest) {
	case 0:
		return "origin", nil
	case 1:
		return rest[0], nil
	default:
		return "", errors.New(usage)
	}
}

// printSyncSummary reports the refs a push or pull transferred, or that
// everything was already in sync when none changed.
func printSyncSummary(w *os.File, verb string, refs []string) {
	if len(refs) == 0 {
		fmt.Fprintln(w, "already up to date")
		return
	}
	fmt.Fprintf(w, "%s %d ref(s):\n", verb, len(refs))
	for _, ref := range refs {
		fmt.Fprintf(w, "  %s\n", ref)
	}
}
