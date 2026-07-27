//go:build !windows

package gitremote

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/joakimcarlsson/wasa-cli/internal/link/repotoken"
)

// CredentialCommand is the wasa subcommand git runs as a credential helper.
// It is named the way git names credential helpers so it reads as one in a
// config value or a process listing.
const CredentialCommand = "git-credential-" + Scheme

// CredentialUser is the username presented alongside a repo-scoped token. The
// core reads the token out of either half of a Basic credential and ignores the
// name, so it is a label rather than an identity — the token is what names the
// principal.
const CredentialUser = Scheme

// CredentialGet is the only operation that mints anything. git also asks a
// helper to store a credential it accepted and to erase one it did not, and
// both are no-ops here: a token this short-lived is cheaper to mint again than
// to keep.
const CredentialGet = "get"

// TokenSource mints a token authorizing one action against one repo.
// *repotoken.Cache satisfies it.
type TokenSource interface {
	Token(
		ctx context.Context,
		audience string,
		action repotoken.Action,
	) (string, error)
}

// CredentialRequest is one credential.helper invocation.
type CredentialRequest struct {
	// Operation is the word git passed as the last argument: get, store or
	// erase.
	Operation string
	// Audience is the repo the token is minted for, as the helper resolved it.
	Audience string
	// ActionFile is where the helper recorded the direction of the transfer.
	ActionFile string
	// Tokens mints the token.
	Tokens TokenSource
	// Stdin and Stdout are git's credential protocol.
	Stdin  io.Reader
	Stdout io.Writer
}

// Credential answers one credential request from the transport with a token
// scoped to this repo and to the direction the transfer turned out to be.
//
// The account's login JWT stays behind the token source and is never what git
// presents: a credential captured from this exchange is worth one repo and one
// action for the few minutes the core minted it for.
func Credential(ctx context.Context, req CredentialRequest) error {
	if err := drain(req.Stdin); err != nil {
		return err
	}
	if req.Operation != CredentialGet {
		return nil
	}
	switch {
	case req.Audience == "":
		return errors.New("gitremote: no repo to mint a credential for")
	case req.Tokens == nil:
		return errors.New("gitremote: no token source")
	case req.Stdout == nil:
		return errors.New("gitremote: nowhere to answer git")
	}

	action, err := ReadAction(req.ActionFile)
	if err != nil {
		return err
	}
	token, err := req.Tokens.Token(ctx, req.Audience, action)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(
		req.Stdout, "username=%s\npassword=%s\n", CredentialUser, token,
	)
	if err != nil {
		return fmt.Errorf("gitremote: answer git: %w", err)
	}
	return nil
}

// drain reads the request git wrote. Its contents describe the URL being
// authenticated, which this helper already knows from its own arguments — but
// the stream is read to its end anyway, so git is never left writing into a
// pipe nobody is reading.
func drain(r io.Reader) error {
	if r == nil {
		return nil
	}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		if scanner.Text() == "" {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("gitremote: read git's credential request: %w", err)
	}
	return nil
}
