package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/joakimcarlsson/wasa-cli/internal/link/auth"
	"github.com/joakimcarlsson/wasa-cli/internal/link/core"
	"github.com/joakimcarlsson/wasa-cli/internal/link/gitremote"
	"github.com/joakimcarlsson/wasa-cli/internal/link/identity"
	"github.com/joakimcarlsson/wasa-cli/internal/link/repotoken"
)

func init() {
	commands = append(commands,
		&Command{
			Name:    gitremote.BinaryName,
			Summary: "git remote helper for wasa:// remotes",
			Hidden:  true,
			Run:     runGitRemoteHelper,
		},
		&Command{
			Name:    gitremote.CredentialCommand,
			Summary: "git credential helper for wasa:// remotes",
			Hidden:  true,
			Run:     runGitCredential,
		},
	)
}

const gitRemoteUsage = "usage: " + programName + " " +
	gitremote.BinaryName + " [<remote>] <url>"

// runGitRemoteHelper carries one wasa:// remote's fetch or push to the core of
// the current login. git invokes it by executable name, so the same code is
// reached through a git-remote-wasa symlink to wasa; the subcommand is what
// makes it callable directly.
//
// git passes the remote's nickname and its URL, or the URL alone when the
// remote has no name. No token is minted here: the credential is asked for by
// the transport once it knows whether this is a fetch or a push, and answered
// by the credential subcommand below.
func runGitRemoteHelper(args []string) error {
	var remote, rawURL string
	switch len(args) {
	case 1:
		rawURL = args[0]
	case 2:
		remote, rawURL = args[0], args[1]
	default:
		return errors.New(gitRemoteUsage)
	}

	coreURL, err := helperCore(remote)
	if err != nil {
		return err
	}
	target, err := gitremote.Resolve(coreURL, rawURL)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate the wasa binary: %w", err)
	}

	action, err := gitremote.NewActionFile()
	if err != nil {
		return err
	}
	defer func() { _ = action.Remove() }()

	return gitremote.Run(context.Background(), gitremote.Options{
		Remote: remote,
		Target: target,
		Action: action,
		Credential: gitremote.CredentialConfig(
			exe, coreURL, target.Audience, action.Path(),
		),
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
}

// helperCore returns the core a transfer goes to: the one pinned on the
// remote git invoked the helper for, and otherwise the current login's.
//
// The pin is what `wasa link` writes, and it outranks the current context on
// purpose. A workspace linked to one core, with the context pointed at
// another, would otherwise push to the second — 404ing on a repo that is not
// there, or worse, reaching a repo of the same slug that is. Where a
// workspace's record lives is the link's decision; the context only decides
// who the transfer acts as.
func helperCore(remote string) (string, error) {
	if pinned := gitremote.ConfiguredCore(".", remote); pinned != "" {
		return pinned, nil
	}
	current, err := currentContext()
	if err != nil {
		return "", err
	}
	return current.CoreURL, nil
}

const gitCredentialUsage = "usage: " + programName + " " +
	gitremote.CredentialCommand +
	" --core <url> --audience <audience> --action-file <path> <operation>"

// runGitCredential answers the transport's credential request with a token
// scoped to one repo and to the direction the helper recorded. git spawns it
// per request, so it holds no state of its own beyond the login it reads.
func runGitCredential(args []string) error {
	fs := newFlagSet(programName + " " + gitremote.CredentialCommand)
	coreURL := fs.String("core", "", "core the token is exchanged at")
	audience := fs.String("audience", "", "repo the token is minted for")
	actionFile := fs.String(
		"action-file", "", "file the transfer direction was recorded in",
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New(gitCredentialUsage)
	}

	tokens, err := coreTokens(*coreURL)
	if err != nil {
		return err
	}
	return gitremote.Credential(
		context.Background(),
		gitremote.CredentialRequest{
			Operation:  fs.Arg(0),
			Audience:   *audience,
			ActionFile: *actionFile,
			Tokens:     tokens,
			Stdin:      os.Stdin,
			Stdout:     os.Stdout,
		},
	)
}

// errNotLoggedIn is what every command on the control-plane path reports when
// there is no identity to act as. It names the one cure rather than the
// missing file.
var errNotLoggedIn = errors.New(
	"not logged in — run `" + programName + " login --core <url>`",
)

// currentContext returns the active login context, or errNotLoggedIn.
func currentContext() (identity.Context, error) {
	store, err := auth.Default()
	if err != nil {
		return identity.Context{}, err
	}
	current, err := store.Current()
	if errors.Is(err, auth.ErrNotLoggedIn) {
		return identity.Context{}, errNotLoggedIn
	}
	return current, err
}

// coreTokens returns the repo-token cache for one core: exchanges go to
// coreURL, or to the current context's core when it is empty, and the login
// JWT is re-read (and refreshed) at each exchange rather than captured now.
func coreTokens(coreURL string) (*repotoken.Cache, error) {
	store, err := auth.Default()
	if err != nil {
		return nil, err
	}
	current, err := store.Current()
	if errors.Is(err, auth.ErrNotLoggedIn) {
		return nil, errNotLoggedIn
	}
	if err != nil {
		return nil, err
	}
	if coreURL == "" {
		coreURL = current.CoreURL
	}
	client, err := core.New(coreURL)
	if err != nil {
		return nil, err
	}
	return repotoken.NewCache(
		client,
		func(ctx context.Context) (string, error) {
			_, access, err := store.Access(ctx, time.Now())
			if errors.Is(err, auth.ErrNotLoggedIn) {
				return "", errNotLoggedIn
			}
			return access.Value, err
		},
	), nil
}
