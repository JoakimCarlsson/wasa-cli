package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/joakimcarlsson/wasa/internal/profile"
	"github.com/joakimcarlsson/wasa/internal/registry"
	"github.com/joakimcarlsson/wasa/internal/tmux"
)

// defaultSessionProgram is the program a session runs when none is given on the
// command line. wasa is a cockpit for AI coding agents, so the default is the
// claude agent rather than a bare shell.
const defaultSessionProgram = "claude"

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

const workspaceUsage = "usage: wasa workspace <list|current|profiles>"

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
	default:
		return fmt.Errorf(
			"unknown workspace subcommand %q\n%s",
			sub,
			workspaceUsage,
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
		defaultSessionProgram,
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

	prof, err := current.SelectProfile(profileName)
	if err != nil {
		return err
	}

	env, err := profile.Resolve(prof, program)
	if err != nil {
		return err
	}

	m, err := newManager()
	if err != nil {
		return err
	}
	worktreePath, err := m.Add(branch)
	if err != nil {
		return err
	}

	sessionID := registry.NewSessionID()
	tmuxName := registry.TmuxName(current.ID, sessionID)
	if err := tmux.New().SpawnEnv(tmuxName, worktreePath, env, program); err != nil {
		return err
	}

	reg.AddSession(&registry.Session{
		ID:           sessionID,
		WorkspaceID:  current.ID,
		ProfileName:  prof.Name,
		Title:        title,
		Program:      program,
		Branch:       branch,
		WorktreePath: worktreePath,
		TmuxName:     tmuxName,
	})
	if err := reg.Save(); err != nil {
		return err
	}

	fmt.Fprintln(os.Stdout, tmuxName)
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
		ws, created := reg.EnsureWorkspace(
			repoPath,
			remoteURL,
			filepath.Base(repoPath),
		)
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
