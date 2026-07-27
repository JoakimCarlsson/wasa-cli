//go:build !windows

package gitremote

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/joakimcarlsson/wasa-cli/internal/link/repotoken"
)

// stubTokens records what it was asked for and hands back a fixed token.
type stubTokens struct {
	audience string
	action   repotoken.Action
	calls    int
	err      error
}

func (s *stubTokens) Token(
	_ context.Context,
	audience string,
	action repotoken.Action,
) (string, error) {
	s.calls++
	s.audience, s.action = audience, action
	if s.err != nil {
		return "", s.err
	}
	return "scoped-token", nil
}

func credentialRequest(
	t *testing.T,
	action repotoken.Action,
	op string,
	tokens TokenSource,
	out *strings.Builder,
) error {
	t.Helper()
	f, err := NewActionFile()
	if err != nil {
		t.Fatalf("NewActionFile: %v", err)
	}
	t.Cleanup(func() { _ = f.Remove() })
	if action != "" {
		if err := f.Write(action); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	return Credential(context.Background(), CredentialRequest{
		Operation:  op,
		Audience:   "/et/acme/widgets",
		ActionFile: f.Path(),
		Tokens:     tokens,
		Stdin: strings.NewReader(
			"protocol=https\nhost=core.example\n\n",
		),
		Stdout: out,
	})
}

func TestCredentialAnswersWithAScopedToken(t *testing.T) {
	for _, action := range []repotoken.Action{repotoken.Pull, repotoken.Push} {
		tokens := &stubTokens{}
		var out strings.Builder
		err := credentialRequest(t, action, CredentialGet, tokens, &out)
		if err != nil {
			t.Fatalf("Credential: %v", err)
		}
		want := "username=" + CredentialUser + "\npassword=scoped-token\n"
		if out.String() != want {
			t.Errorf("answer = %q, want %q", out.String(), want)
		}
		if tokens.action != action {
			t.Errorf("minted for %q, want %q", tokens.action, action)
		}
		if tokens.audience != "/et/acme/widgets" {
			t.Errorf("minted for audience %q", tokens.audience)
		}
	}
}

func TestCredentialMintsNothingForStoreAndErase(t *testing.T) {
	for _, op := range []string{"store", "erase"} {
		tokens := &stubTokens{}
		var out strings.Builder
		if err := credentialRequest(
			t, repotoken.Push, op, tokens, &out,
		); err != nil {
			t.Fatalf("Credential(%s): %v", op, err)
		}
		if tokens.calls != 0 {
			t.Errorf("%s minted %d token(s)", op, tokens.calls)
		}
		if out.String() != "" {
			t.Errorf("%s answered %q", op, out.String())
		}
	}
}

func TestCredentialRefusesWithoutADirection(t *testing.T) {
	tokens := &stubTokens{}
	var out strings.Builder
	err := credentialRequest(t, "", CredentialGet, tokens, &out)
	if err == nil {
		t.Fatal("Credential with no recorded direction: want an error")
	}
	if tokens.calls != 0 {
		t.Errorf("a token was minted anyway (%d call(s))", tokens.calls)
	}
}

func TestCredentialReportsAFailedExchange(t *testing.T) {
	tokens := &stubTokens{err: errors.New("no access")}
	var out strings.Builder
	err := credentialRequest(t, repotoken.Pull, CredentialGet, tokens, &out)
	if err == nil {
		t.Fatal("Credential with a failing exchange: want an error")
	}
	if out.String() != "" {
		t.Errorf("answered %q despite the failure", out.String())
	}
}

func TestCredentialNeedsARepoAndASource(t *testing.T) {
	var out strings.Builder
	err := Credential(context.Background(), CredentialRequest{
		Operation: CredentialGet,
		Stdout:    &out,
	})
	if err == nil {
		t.Error("Credential with no audience: want an error")
	}

	err = Credential(context.Background(), CredentialRequest{
		Operation: CredentialGet,
		Audience:  "/et/acme/widgets",
		Stdout:    &out,
	})
	if err == nil {
		t.Error("Credential with no token source: want an error")
	}
}
