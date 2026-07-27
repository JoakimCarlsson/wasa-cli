//go:build !windows

package gitremote

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joakimcarlsson/wasa-cli/internal/link/repotoken"
)

// actionFileName is the name the direction is recorded under inside the
// helper's private directory.
const actionFileName = "action"

// ActionFile carries the direction of a transfer from the helper to the
// credential helper git spawns underneath it.
//
// It exists because the two facts arrive at different times and in different
// processes: git reveals whether it is fetching or pushing partway through the
// conversation, and the credential is asked for by a grandchild whose
// environment was fixed before that. Only the direction is written — a token
// never touches the disk.
type ActionFile struct {
	dir  string
	path string
}

// NewActionFile creates the file's private directory. Both the directory and
// the file are the invoking user's alone: the direction is not a secret, but a
// direction another user could rewrite would decide which scope this transfer
// asks for.
func NewActionFile() (*ActionFile, error) {
	dir, err := os.MkdirTemp("", "wasa-git-remote-")
	if err != nil {
		return nil, fmt.Errorf("gitremote: %w", err)
	}
	return &ActionFile{dir: dir, path: filepath.Join(dir, actionFileName)}, nil
}

// Path returns the file's location, which is what the credential helper is
// told to read.
func (f *ActionFile) Path() string { return f.path }

// Write records the direction. It must return before the command that revealed
// it reaches git's transport, so the credential helper can never observe the
// file as absent once a request is in flight.
func (f *ActionFile) Write(action repotoken.Action) error {
	err := os.WriteFile(f.path, []byte(action), 0o600)
	if err != nil {
		return fmt.Errorf("gitremote: record the transfer direction: %w", err)
	}
	return nil
}

// Remove deletes the file and its directory.
func (f *ActionFile) Remove() error {
	return os.RemoveAll(f.dir)
}

// ReadAction reads back the direction a helper recorded. A missing file means
// git asked for a credential before it said what the transfer is, which is not
// something to guess at: an empty answer is safer than minting push authority
// for a request that never asked for it.
func ReadAction(path string) (repotoken.Action, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf(
			"gitremote: could not tell whether this is a fetch or a push: %w",
			err,
		)
	}
	switch action := repotoken.Action(strings.TrimSpace(string(raw))); action {
	case repotoken.Pull, repotoken.Push:
		return action, nil
	default:
		return "", fmt.Errorf(
			"gitremote: %q is not a transfer direction", action,
		)
	}
}

// directionOf reports the scope a remote-helper command implies, and whether
// it implies one at all.
//
// The commands that reveal it are the ones that start a transfer, and each is
// unambiguous about which half of the protocol it opens: reading refs or
// objects needs pull, advertising or sending them for a push needs push. The
// core enforces the same mapping on its side, so a token minted from the wrong
// one is refused rather than quietly over-privileged.
func directionOf(line string) (repotoken.Action, bool) {
	command, rest, _ := strings.Cut(strings.TrimSpace(line), " ")
	switch command {
	case "list":
		if strings.TrimSpace(rest) == "for-push" {
			return repotoken.Push, true
		}
		return repotoken.Pull, true
	case "fetch", "get":
		return repotoken.Pull, true
	case "push":
		return repotoken.Push, true
	case "stateless-connect", "connect":
		return serviceDirection(strings.TrimSpace(rest))
	default:
		return "", false
	}
}

// serviceDirection maps the git service a connect command opens onto a scope.
// An unknown service reveals nothing, so the conversation continues and the
// child answers for it.
func serviceDirection(service string) (repotoken.Action, bool) {
	switch service {
	case "git-upload-pack", "git-upload-archive":
		return repotoken.Pull, true
	case "git-receive-pack":
		return repotoken.Push, true
	default:
		return "", false
	}
}
