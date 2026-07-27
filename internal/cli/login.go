package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/joakimcarlsson/wasa-cli/internal/link/auth"
	"github.com/joakimcarlsson/wasa-cli/internal/link/core"
)

func init() {
	commands = append(commands,
		&Command{
			Name:    "login",
			Summary: "log in to a wasa-api core in the browser",
			Run:     runLogin,
		},
		&Command{
			Name:    "logout",
			Summary: "discard the current login",
			Run:     runLogout,
		},
		&Command{
			Name:    "whoami",
			Summary: "show the current login context and principal",
			Run:     runWhoami,
		},
	)
}

// coreEnv names the environment variable holding the default core URL, so a
// user with one core does not repeat --core on every login.
const coreEnv = "WASA_CORE"

const loginUsage = "usage: wasa login " +
	"[--core <url>] [--name <context>] [--provider <name>]"

const loginHelp = `usage: wasa login [--core <url>] [--name <context>] [--provider <name>]

Log in to a wasa-api core. wasa opens your browser at the core's login
endpoint and waits on a loopback listener for the core to hand back its own
login token and refresh token, which are stored in your OS keychain under a
named context and become the identity later commands act as.

The core defaults to $WASA_CORE; the context name defaults to the core's host.
Logging in again for the same context replaces its tokens. Nothing about
offline use changes: with no login, every other command behaves as before.

Flags:
  --core <url>       the wasa-api core to log in to (default $WASA_CORE)
  --name <context>   name to store the login under (default the core's host)
  --provider <name>  login provider to use (default github)
`

func runLogin(args []string) error {
	fs := newFlagSet("wasa login")
	coreURL := fs.String("core", os.Getenv(coreEnv), "wasa-api core URL")
	name := fs.String("name", "", "context name to store the login under")
	provider := fs.String("provider", core.DefaultProvider, "login provider")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, loginHelp)
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New(loginUsage)
	}
	if *coreURL == "" {
		return fmt.Errorf(
			"no core to log in to: pass --core <url> or set %s", coreEnv,
		)
	}

	client, err := core.New(*coreURL)
	if err != nil {
		return err
	}
	store, err := auth.Default()
	if err != nil {
		return err
	}
	return login(
		context.Background(), client, store, *name,
		core.LoginOptions{Provider: *provider, Prompt: os.Stdout},
		os.Stdout,
	)
}

// login runs the browser round-trip, confirms who the resulting token names,
// and records both under a context. The principal comes from the core rather
// than from the token, so wasa never parses a JWT it does not issue.
func login(
	ctx context.Context,
	client *core.Client,
	store *auth.Store,
	name string,
	opts core.LoginOptions,
	out io.Writer,
) error {
	tokens, err := core.Login(ctx, client, opts)
	if err != nil {
		return err
	}

	principal, err := client.Me(ctx, tokens.Access)
	if err != nil {
		return err
	}
	if name == "" {
		name = client.Host()
	}

	saved, err := store.Save(auth.Login{
		Name:      name,
		CoreURL:   client.BaseURL(),
		Principal: principal.String(),
		Tokens:    tokens,
	}, time.Now())
	if err != nil {
		return err
	}

	fmt.Fprintf(
		out,
		"logged in to %s as %s (context %s)\n",
		saved.CoreURL, saved.Principal, saved.Name,
	)
	return nil
}

const logoutUsage = "usage: wasa logout"

func runLogout(args []string) error {
	fs := newFlagSet("wasa logout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New(logoutUsage)
	}

	store, err := auth.Default()
	if err != nil {
		return err
	}
	name, err := store.Logout()
	if errors.Is(err, auth.ErrNotLoggedIn) {
		fmt.Fprintln(os.Stdout, "not logged in")
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "logged out of context %s\n", name)
	return nil
}

const whoamiUsage = "usage: wasa whoami"

func runWhoami(args []string) error {
	fs := newFlagSet("wasa whoami")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New(whoamiUsage)
	}

	store, err := auth.Default()
	if err != nil {
		return err
	}
	return whoami(context.Background(), store, os.Stdout)
}

// whoami reports the active identity, refreshing the login token first when it
// is near expiry so the answer reflects a credential that still works. The
// principal is re-read from the core and written back, so a renamed account
// shows up rather than being served from a stale context.
func whoami(ctx context.Context, store *auth.Store, out io.Writer) error {
	current, access, err := store.Access(ctx, time.Now())
	if errors.Is(err, auth.ErrNotLoggedIn) {
		return fmt.Errorf(
			"not logged in — run `wasa login --core <url>`",
		)
	}
	if err != nil {
		return err
	}

	client, err := core.New(current.CoreURL)
	if err != nil {
		return err
	}
	principal, err := client.Me(ctx, access.Value)
	switch {
	case errors.Is(err, core.ErrUnauthorized):
		return fmt.Errorf(
			"the login for context %s is no longer accepted — "+
				"run `wasa login --core %s`",
			current.Name, current.CoreURL,
		)
	case err != nil:
		return err
	}
	if principal.String() != current.Principal {
		if err := store.SetPrincipal(
			current.Name, principal.String(),
		); err != nil {
			return err
		}
	}

	fmt.Fprintf(out, "context:   %s\n", current.Name)
	fmt.Fprintf(out, "core:      %s\n", current.CoreURL)
	fmt.Fprintf(out, "principal: %s\n", principal)
	return nil
}
