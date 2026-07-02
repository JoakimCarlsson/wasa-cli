package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/joakimcarlsson/wasa-cli/internal/backend"
	"github.com/joakimcarlsson/wasa-cli/internal/launch"
	"github.com/joakimcarlsson/wasa-cli/internal/registry"
	"github.com/joakimcarlsson/wasa-cli/internal/repo"
)

func init() {
	commands = append(commands,
		&Command{
			Name:    "workspace",
			Summary: "list, add, remove and resolve per-repository workspaces",
			Run:     runWorkspace,
		},
		&Command{
			Name:    "session",
			Summary: "list agent sessions",
			Run:     runSession,
		},
	)
}

const workspaceUsage = "usage: wasa workspace <list|current|profiles|add|remove>"

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
	case "remove":
		return workspaceRemove(rest)
	default:
		return fmt.Errorf(
			"unknown workspace subcommand %q\n%s",
			sub,
			workspaceUsage,
		)
	}
}

const workspaceAddHelp = `usage: wasa workspace add [--init] <path>

Register an existing git repository as a workspace so it shows up in the cockpit
without first cd-ing into it. <path> is canonicalized (symlinks resolved, made
absolute), its primary remote is read, and it is registered with one default
profile under the same content-addressed id that in-repo auto-registration uses.
Adding an already-registered repository is idempotent: it is not duplicated.

Without --init, <path> must already be a git repository; a missing or non-git
path is reported as an error.

With --init, a new or not-yet-versioned project is bootstrapped into a workspace:
a missing <path> is created (mkdir -p), a non-git directory is initialized
(git init), and the result is registered. The new repository has no commits, so
worktree sessions are unavailable until the first commit, but the workspace hosts
plain sessions at once. Initializing a directory that is already a repository is
a no-op beyond registering it.
`

func workspaceAdd(args []string) error {
	fs := newFlagSet("wasa workspace add")
	var doInit bool
	fs.BoolVar(
		&doInit,
		"init",
		false,
		"create and git-init the path if it is not already a git repository",
	)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, workspaceAddHelp)
			return nil
		}
		return err
	}

	rest := fs.Args()
	if len(rest) != 1 {
		return errors.New("usage: wasa workspace add [--init] <path>")
	}

	reg, err := registry.Open(wasaHome())
	if err != nil {
		return err
	}

	ws, _, err := addWorkspace(reg, rest[0], doInit)
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
// duplicated.
//
// With doInit false it errors when path does not exist or is not a git
// repository, and never creates the path. With doInit true it bootstraps instead:
// a missing path is created (mkdir -p) and a non-git directory is git-initialized
// before registering, so a brand-new project becomes a workspace in one command.
func addWorkspace(
	reg *registry.Registry,
	path string,
	doInit bool,
) (*registry.Workspace, bool, error) {
	if _, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			return nil, false, err
		}
		if !doInit {
			return nil, false, fmt.Errorf(
				"%s does not exist; pass --init to create and initialize it, "+
					"or add an existing git repository",
				path,
			)
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			return nil, false, err
		}
	}

	if doInit {
		if _, _, err := resolveRepo(path); err != nil {
			if err := repo.Init(path); err != nil {
				return nil, false, err
			}
		}
	}

	repoPath, remoteURL, err := resolveRepo(path)
	if err != nil {
		return nil, false, err
	}

	ws, created := registerRepo(reg, repoPath, remoteURL)
	return ws, created, nil
}

const workspaceRemoveHelp = `usage: wasa workspace remove <path|id>

Remove a workspace from wasa, identified by its repository <path> or its
workspace id (a unique id prefix also works); see "wasa workspace list".

This cascades: every session the workspace owns is torn down first — its tmux is
stopped, its worktree is removed and its branch is force-deleted — exactly as
"wasa finish --force" would do, discarding any uncommitted or unmerged work on
those branches. The workspace is then dropped from the registry.

The repository on disk is never touched: removing a workspace only makes wasa
forget it. Re-add it any time with "wasa workspace add <path>".
`

func workspaceRemove(args []string) error {
	fs := newFlagSet("wasa workspace remove")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, workspaceRemoveHelp)
			return nil
		}
		return err
	}

	rest := fs.Args()
	if len(rest) != 1 {
		return errors.New("usage: wasa workspace remove <path|id>")
	}

	reg, err := registry.Open(wasaHome())
	if err != nil {
		return err
	}

	ws, err := resolveWorkspace(reg, rest[0])
	if err != nil {
		return err
	}

	n, err := launch.DeleteWorkspace(reg, backend.Default(), wasaHome(), ws)
	if err != nil {
		return fmt.Errorf("tear down workspace %s: %w", ws.Name, err)
	}
	if err := reg.Save(); err != nil {
		return err
	}

	fmt.Fprintf(
		os.Stdout,
		"removed workspace %s (%d session(s) torn down)\n",
		ws.Name,
		n,
	)
	return nil
}

// resolveWorkspace finds the workspace referenced by query, matched first as an
// exact workspace id, then as a repository path (resolved to its workspace id),
// then as a unique id prefix. It errors when nothing matches and when an id
// prefix is ambiguous, rather than guessing. Unlike workspaceForDir it never
// registers: a path that is not already a workspace is reported as no match.
func resolveWorkspace(
	reg *registry.Registry,
	query string,
) (*registry.Workspace, error) {
	if query == "" {
		return nil, errors.New("a workspace path or id is required")
	}
	if w, ok := reg.Workspace(query); ok {
		return w, nil
	}
	if repoPath, remoteURL, err := resolveRepo(query); err == nil {
		if w, ok := reg.Workspace(
			registry.WorkspaceID(repoPath, remoteURL),
		); ok {
			return w, nil
		}
	}

	var matches []*registry.Workspace
	for _, w := range reg.ListWorkspaces() {
		if strings.HasPrefix(w.ID, query) {
			matches = append(matches, w)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no workspace matches %q", query)
	case 1:
		return matches[0], nil
	default:
		return nil, fmt.Errorf(
			"%q is ambiguous: %d workspaces match; use a full id",
			query, len(matches),
		)
	}
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

func sessionNew(args []string) error {
	fs := newFlagSet("wasa session new")
	var profileName, program, branch, title, dir string
	fs.StringVar(
		&profileName,
		"profile",
		"",
		"profile name (default profile if unset)",
	)
	fs.StringVar(
		&program,
		"program",
		"",
		"program to run (default: the sole detected agent, else the shell)",
	)
	fs.StringVar(
		&branch,
		"branch",
		"",
		"branch to create a worktree on; omit for a plain session in --dir",
	)
	fs.StringVar(
		&dir,
		"dir",
		"",
		"working directory for a plain session (default: current directory)",
	)
	fs.StringVar(&title, "title", "", "human-readable session title")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if program == "" {
		resolved, err := resolveProgram()
		if err != nil {
			return err
		}
		program = resolved
	}

	reg, current, err := openRegistry()
	if err != nil {
		return err
	}

	var (
		ws     *registry.Workspace
		params launch.Params
	)
	if branch != "" {
		if current == nil {
			return errors.New("not a git repository")
		}
		ws = current
		params = launch.Params{
			Branch:  branch,
			Title:   title,
			Program: program,
			Profile: profileName,
		}
	} else {
		workdir, derr := resolvePlainDir(dir)
		if derr != nil {
			return derr
		}
		ws = workspaceForDir(reg, workdir)
		params = launch.Params{
			Title:      title,
			Program:    program,
			Profile:    profileName,
			WorkingDir: workdir,
		}
	}

	s, err := launch.CreateSession(wasaHome(), reg, ws, params)
	if err != nil {
		return err
	}
	if err := reg.Save(); err != nil {
		return err
	}

	fmt.Fprintln(os.Stdout, s.TmuxName)
	return nil
}

// resolvePlainDir resolves the working directory for a plain session. An empty
// dir defaults to the current directory; an explicit dir must exist and be a
// directory, and is returned as an absolute path.
func resolvePlainDir(dir string) (string, error) {
	if dir == "" {
		return os.Getwd()
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", dir)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return abs, nil
}

// workspaceForDir returns the registered workspace whose repository contains
// dir, registering it on first use, or nil when dir is not inside a git
// repository. It mirrors openRegistry's in-repo auto-registration but for an
// explicit directory, so a plain session launched inside a known repository
// still attaches to its workspace (and profile environment) while one launched
// outside any repository runs with no workspace at all.
func workspaceForDir(reg *registry.Registry, dir string) *registry.Workspace {
	repoPath, remoteURL, err := resolveRepo(dir)
	if err != nil {
		return nil
	}
	ws, _ := registerRepo(reg, repoPath, remoteURL)
	return ws
}

// resolveProgram picks the program for a session new invocation that omitted
// --program. It uses the sole detected agent when exactly one is on PATH,
// refuses to guess when several are (the caller must pass --program), and falls
// back to the OS shell when none are installed.
func resolveProgram() (string, error) {
	agents := launch.DetectAgents()
	switch len(agents) {
	case 0:
		return launch.Shell(), nil
	case 1:
		return agents[0], nil
	default:
		return "", fmt.Errorf(
			"multiple agents found on PATH (%s); "+
				"pass --program to choose one",
			strings.Join(agents, ", "),
		)
	}
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

	client := backend.Default()
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
