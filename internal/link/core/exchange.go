package core

import (
	"context"
	"strings"
	"time"

	"github.com/joakimcarlsson/wasa-api/pkg/proto"
)

// GrantTypeTokenExchange is the RFC 8693 grant the core's /oauth/token
// endpoint implements. It is the only grant it accepts.
const GrantTypeTokenExchange = "urn:ietf:params:oauth:grant-type:token-exchange"

// ScopedToken is what an exchange yields: a bearer token good for one repo and
// one action, and short-lived enough that a leaked copy is worth little.
type ScopedToken struct {
	Value     string
	Scope     string
	ExpiresIn time.Duration
}

// RepoAudience is the audience naming a repo by its id — the canonical form,
// used once the repo's ULID is known.
func RepoAudience(repoID string) string {
	return "/git/repo/" + strings.TrimSuffix(repoID, ".git")
}

// SlugAudience is the audience naming a repo the way a git remote addresses
// it, for the push and fetch path where only owner and name are known. It
// resolves to the same repo as RepoAudience.
func SlugAudience(owner, name string) string {
	return "/et/" + owner + "/" + strings.TrimSuffix(name, ".git")
}

// Exchange trades a login JWT for a token scoped to one repo and one action.
//
// The login JWT is sent as the RFC 8693 subject token and never leaves this
// call: what the caller hands onward to a git host is the minted token, which
// carries authority over that repo alone. audience is one of the two shapes
// RepoAudience and SlugAudience build; scope is "pull" or "push".
//
// A core that refuses the subject token reports ErrUnauthorized; one that
// accepts it but denies the action reports ErrForbidden.
func (c *Client) Exchange(
	ctx context.Context,
	loginJWT, audience, scope string,
) (ScopedToken, error) {
	out, err := c.api.PostOauthToken(ctx, proto.PostOauthTokenParams{
		GrantType:    GrantTypeTokenExchange,
		SubjectToken: loginJWT,
		Audience:     audience,
		Scope:        scope,
	})
	if err != nil {
		return ScopedToken{}, c.asError(err)
	}
	return ScopedToken{
		Value:     out.AccessToken,
		Scope:     out.Scope,
		ExpiresIn: time.Duration(out.ExpiresIn) * time.Second,
	}, nil
}
