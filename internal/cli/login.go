package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/joakimcarlsson/wasa-cli/internal/link"
)

func init() {
	commands = append(commands,
		&Command{
			Name:    "login",
			Summary: "link this runner to the hosted control plane",
			Run:     runLogin,
		},
		&Command{
			Name:    "logout",
			Summary: "unlink this runner and delete its stored credential",
			Run:     runLogout,
		},
	)
}

const loginHelp = `usage: wasa login [--url <origin>] [--name <name>]

Link this runner to the control plane. Prints a short code and opens the
verify page in your browser; confirm the code there matches this terminal and
approve. The runner token is stored under $WASA_HOME with owner-only
permissions.

Flags:
  --url        control plane origin (default ` + link.DefaultOrigin + `)
  --name       runner name shown on the dashboard (default: this hostname)
  -h, --help   show this help and exit
`

const logoutHelp = `usage: wasa logout

Delete the stored credential and revoke this runner server-side
(best-effort — the credential is removed locally either way).

Flags:
  -h, --help   show this help and exit
`

func runLogin(args []string) error {
	fs := newFlagSet("wasa login")
	var origin, name string
	fs.StringVar(&origin, "url", link.DefaultOrigin, "control plane origin")
	fs.StringVar(&name, "name", "", "runner name (default: hostname)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, loginHelp)
			return nil
		}
		return err
	}
	if len(fs.Args()) != 0 {
		return errors.New("usage: wasa login [--url <origin>] [--name <name>]")
	}
	if name == "" {
		name = link.Hostname()
	}

	ctx := context.Background()
	started, err := link.StartLink(ctx, origin, name)
	if err != nil {
		return fmt.Errorf("could not reach the control plane: %w", err)
	}

	fmt.Fprintf(os.Stdout,
		"Confirm this code in your browser: %s\n\n  %s\n\n",
		started.UserCode, started.VerifyURL,
	)
	openBrowser(started.VerifyURL)
	fmt.Fprintln(os.Stdout, "Waiting for approval...")

	interval := time.Duration(started.Interval) * time.Second
	deadline := time.Now().Add(time.Duration(started.ExpiresIn) * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(interval)
		res, err := link.PollLink(ctx, origin, started.DeviceSecret)
		if err != nil {
			continue
		}
		switch res.Status {
		case link.StatusPending:
		case link.StatusApproved:
			creds := link.Credentials{URL: origin, Token: res.Token}
			if err := link.SaveCredentials(wasaHome(), creds); err != nil {
				return fmt.Errorf("save credentials: %w", err)
			}
			fmt.Fprintf(os.Stdout,
				"Linked as %q. The runner connects whenever wasa is running.\n",
				name,
			)
			return nil
		case link.StatusDenied:
			return errors.New("the link request was denied in the browser")
		default:
			return errors.New(
				"the link request expired; run `wasa login` again",
			)
		}
	}
	return errors.New("the link request expired; run `wasa login` again")
}

func runLogout(args []string) error {
	fs := newFlagSet("wasa logout")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, logoutHelp)
			return nil
		}
		return err
	}

	creds, ok, err := link.LoadCredentials(wasaHome())
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(os.Stdout, "not linked; nothing to do")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := link.Revoke(ctx, creds.URL, creds.Token); err != nil {
		fmt.Fprintf(
			os.Stdout,
			"could not revoke server-side (%v); removing the local credential anyway\n",
			err,
		)
	}
	if err := link.DeleteCredentials(wasaHome()); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "logged out")
	return nil
}

// openBrowser best-effort opens url in the user's browser; `wasa login`
// always prints the URL too, for headless and SSH sessions.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
