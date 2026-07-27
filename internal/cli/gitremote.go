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

	current, err := currentContext()
	if err != nil {
		return err
	}
	target, err := gitremote.Resolve(current.CoreURL, rawURL)
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
			exe, target.Audience, action.Path(),
		),
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
}

const gitCredentialUsage = "usage: " + programName + " " +
	gitremote.CredentialCommand +
	" --audience <audience> --action-file <path> <operation>"

// runGitCredential answers the transport's credential request with a token
// scoped to one repo and to the direction the helper recorded. git spawns it
// per request, so it holds no state of its own beyond the login it reads.
func runGitCredential(args []string) error {
	fs := newFlagSet(programName + " " + gitremote.CredentialCommand)
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

	tokens, err := currentTokens()
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

// currentTokens returns the repo-token cache over the current login: exchanges
// go to that context's core, and the login JWT is re-read (and refreshed) at
// each exchange rather than captured now.
func currentTokens() (*repotoken.Cache, error) {
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
	client, err := core.New(current.CoreURL)
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
