package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/joakimcarlsson/wasa/internal/launch"
	"github.com/joakimcarlsson/wasa/internal/registry"
	"github.com/joakimcarlsson/wasa/internal/tmux"
)

func init() {
	commands = append(commands,
		&Command{
			Name:    "workspace",
			Summary: "list and resolve per-repository workspaces",
			Run:     runWorkspace,
		},
		&Command{
			Name:    "session",
			Summary: "list agent sessions",
			Run:     runSession,
		},
	)
}

const workspaceUsage = "usage: wasa workspace <list|current|profiles|add>"

func runWorkspace(args []string) error {
	if len(args) == 0 {
		return errors.New(workspaceUsage)
	}

	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return workspaceList(rest)
	case "current":
		return workspaceCurrent(rest)
	case "profiles":
		return workspaceProfiles(rest)
	case "add":
		return workspaceAdd(rest)
	default:
		return fmt.Errorf(
			"unknown workspace subcommand %q\n%s",
			sub,
			workspaceUsage,
		)
	}
}

const workspaceAddHelp = `usage: wasa workspace add <path>

Register an existing git repository as a workspace so it shows up in the cockpit
without first cd-ing into it. <path> is canonicalized (symlinks resolved, made
absolute), its primary remote is read, and it is registered with one default
profile under the same content-addressed id that in-repo auto-registration uses.
Adding an already-registered repository is idempotent: it is not duplicated.

<path> must already be a git repository. Bootstrapping a new repository from a
non-existent path (mkdir + git init) is out of scope for this release and is
never performed; a missing or non-git path is reported as an error.
`

func workspaceAdd(args []string) error {
	fs := newFlagSet("wasa workspace add")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, workspaceAddHelp)
			return nil
		}
		return err
	}

	rest := fs.Args()
	if len(rest) != 1 {
		return errors.New("usage: wasa workspace add <path>")
	}

	reg, err := registry.Open(wasaHome())
	if err != nil {
		return err
	}

	ws, _, err := addWorkspace(reg, rest[0])
	if err != nil {
		return err
	}
	if err := reg.Save(); err != nil {
		return err
	}

	fmt.Fprintln(os.Stdout, ws.ID)
	return nil
}

// addWorkspace registers the git repository at path in reg, reusing the same
// resolve-and-register path as in-repo auto-registration so the workspace id and
// default profile are identical. It returns the workspace and whether it was
// newly created; an existing repository is returned unchanged rather than
// duplicated. It errors when path does not exist or is not a git repository, and
// never creates the path: path-bootstrap is out of scope for this release.
func addWorkspace(
	reg *registry.Registry,
	path string,
) (*registry.Workspace, bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, false, fmt.Errorf(
				"%s does not exist; workspace add registers an existing git "+
					"repository and never creates one "+
					"(path-bootstrap is out of scope)",
				path,
			)
		}
		return nil, false, err
	}

	repoPath, remoteURL, err := resolveRepo(path)
	if err != nil {
		return nil, false, err
	}

	ws, created := registerRepo(reg, repoPath, remoteURL)
	return ws, created, nil
}

func workspaceList(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: wasa workspace list")
	}

	reg, _, err := openRegistry()
	if err != nil {
		return err
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, w := range reg.ListWorkspaces() {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", w.ID, w.Name, w.RepoPath)
	}
	return tw.Flush()
}

func workspaceCurrent(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: wasa workspace current")
	}

	_, current, err := openRegistry()
	if err != nil {
		return err
	}
	if current == nil {
		return errors.New("not a git repository")
	}
	fmt.Fprintln(os.Stdout, current.ID)
	return nil
}

func workspaceProfiles(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: wasa workspace profiles")
	}

	_, current, err := openRegistry()
	if err != nil {
		return err
	}
	if current == nil {
		return errors.New("not a git repository")
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for i, p := range current.Profiles {
		marker := ""
		if i == 0 {
			marker = "(default)"
		}
		fmt.Fprintf(tw, "%s\t%s\n", p.Name, marker)
	}
	return tw.Flush()
}

const sessionUsage = "usage: wasa session <list|new>"

func runSession(args []string) error {
	if len(args) == 0 {
		return errors.New(sessionUsage)
	}

	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return sessionList(rest)
	case "new":
		return sessionNew(rest)
	default:
		return fmt.Errorf(
			"unknown session subcommand %q\n%s",
			sub,
			sessionUsage,
		)
	}
}

const sessionNewUsage = "usage: wasa session new --branch <branch> " +
	"[--profile <name>] [--program <program>] [--title <title>]"

func sessionNew(args []string) error {
	fs := newFlagSet("wasa session new")
	var profileName, program, branch, title string
	fs.StringVar(
		&profileName,
		"profile",
		"",
		"profile name (default profile if unset)",
	)
	fs.StringVar(
		&program,
		"program",
		launch.DefaultProgram,
		"program to run in the session",
	)
	fs.StringVar(
		&branch,
		"branch",
		"",
		"branch to create the worktree on (required)",
	)
	fs.StringVar(&title, "title", "", "human-readable session title")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if branch == "" {
		return errors.New(sessionNewUsage)
	}

	reg, current, err := openRegistry()
	if err != nil {
		return err
	}
	if current == nil {
		return errors.New("not a git repository")
	}

	s, err := launch.CreateSession(wasaHome(), reg, current, launch.Params{
		Branch:  branch,
		Title:   title,
		Program: program,
		Profile: profileName,
	})
	if err != nil {
		return err
	}
	if err := reg.Save(); err != nil {
		return err
	}

	fmt.Fprintln(os.Stdout, s.TmuxName)
	return nil
}

func sessionList(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: wasa session list")
	}

	reg, _, err := openRegistry()
	if err != nil {
		return err
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, s := range reg.ListSessions() {
		fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\n",
			s.ID,
			s.Title,
			s.Branch,
			s.Status,
		)
	}
	return tw.Flush()
}

// openRegistry loads the registry, reconciles persisted sessions against tmux,
// and silently auto-registers the working directory's repository when it is a
// not-yet-known git repository. It returns the registry and the current
// workspace, which is nil when the working directory is not inside a git
// repository. Any state change is persisted before returning.
func openRegistry() (*registry.Registry, *registry.Workspace, error) {
	reg, err := registry.Open(wasaHome())
	if err != nil {
		return nil, nil, err
	}

	client := tmux.New()
	changed := reg.Reconcile(client.Has)

	var current *registry.Workspace
	if repoPath, remoteURL, rerr := currentRepo(); rerr == nil {
		ws, created := registerRepo(reg, repoPath, remoteURL)
		current = ws
		changed = changed || created
	}

	if changed {
		if err := reg.Save(); err != nil {
			return nil, nil, err
		}
	}
	return reg, current, nil
}
