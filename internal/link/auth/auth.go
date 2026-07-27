//go:build !windows

package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/joakimcarlsson/wasa-cli/internal/link/core"
	"github.com/joakimcarlsson/wasa-cli/internal/link/identity"
)

// ErrNotLoggedIn reports that there is no usable identity: no current context,
// or a context whose tokens are gone. It is the one error every command turns
// into "run wasa login".
var ErrNotLoggedIn = errors.New("not logged in")

// Store owns the two halves of a login — the named context and the tokens
// behind it — and keeps them consistent.
type Store struct {
	contexts *identity.ContextStore
	tokens   *identity.TokenStore
}

// Default returns the store over wasa's own config directory: the same
// contexts.json and keychain slots the git-remote-wasa helper reads.
func Default() (*Store, error) {
	contexts, err := identity.DefaultContextStore()
	if err != nil {
		return nil, err
	}
	tokens, err := identity.NewTokenStore()
	if err != nil {
		return nil, err
	}
	return &Store{contexts: contexts, tokens: tokens}, nil
}

// NewStore returns a store over an explicit context and token store, for tests
// and for callers that keep their state somewhere else.
func NewStore(
	contexts *identity.ContextStore,
	tokens *identity.TokenStore,
) *Store {
	return &Store{contexts: contexts, tokens: tokens}
}

// Login is a completed login ready to be recorded.
type Login struct {
	// Name is the context to store it under.
	Name string
	// CoreURL is the normalized core the tokens are good for.
	CoreURL string
	// Principal is the identity the core says the login JWT names.
	Principal string
	// Tokens is the pair the core handed back.
	Tokens core.Tokens
}

// Save records a completed login: the tokens go to the context's keychain
// slot, the context to contexts.json, and current_context is pointed at it so
// the newest login is the one later commands use.
//
// Tokens are written before the context, so a failure never leaves a context
// advertising a slot that holds nothing.
func (s *Store) Save(l Login, now time.Time) (identity.Context, error) {
	if l.Name == "" {
		return identity.Context{}, errors.New("auth: empty context name")
	}
	ctx := identity.Context{
		Name:         l.Name,
		CoreURL:      l.CoreURL,
		Principal:    l.Principal,
		KeychainSlot: l.Name,
	}
	access, refresh := tokenPair(l.Tokens, now)
	if err := s.tokens.Save(ctx.KeychainSlot, access, refresh); err != nil {
		return identity.Context{}, err
	}
	err := s.contexts.Update(func(c *identity.Contexts) error {
		c.Put(ctx)
		return c.SetCurrent(ctx.Name)
	})
	if err != nil {
		return identity.Context{}, err
	}
	return ctx, nil
}

// Current returns the active context, or ErrNotLoggedIn when current_context
// is unset or dangling.
func (s *Store) Current() (identity.Context, error) {
	doc, err := s.contexts.Load()
	if err != nil {
		return identity.Context{}, err
	}
	ctx, ok := doc.Current()
	if !ok {
		return identity.Context{}, ErrNotLoggedIn
	}
	return ctx, nil
}

// Access returns the current context together with a login JWT that is valid
// now, refreshing it against the core and writing the result back when the
// stored one is inside identity.RefreshBuffer of expiry.
//
// A renewal reissues only the login JWT, so the stored refresh token is kept
// unless the core chose to rotate it. A refresh the core rejects means the
// identity is dead rather than stale, so it reports ErrNotLoggedIn: the only
// cure is another wasa login.
func (s *Store) Access(
	ctx context.Context,
	now time.Time,
) (identity.Context, identity.Token, error) {
	current, err := s.Current()
	if err != nil {
		return identity.Context{}, identity.Token{}, err
	}

	access, refresh, err := s.tokens.Load(current.KeychainSlot)
	if errors.Is(err, identity.ErrNoToken) {
		return identity.Context{}, identity.Token{}, ErrNotLoggedIn
	}
	if err != nil {
		return identity.Context{}, identity.Token{}, err
	}
	if !access.NeedsRefresh(now) {
		return current, access, nil
	}
	if refresh.Value == "" {
		return identity.Context{}, identity.Token{}, ErrNotLoggedIn
	}

	client, err := core.New(current.CoreURL)
	if err != nil {
		return identity.Context{}, identity.Token{}, err
	}
	renewed, err := client.Refresh(ctx, refresh.Value)
	if errors.Is(err, core.ErrUnauthorized) {
		return identity.Context{}, identity.Token{}, fmt.Errorf(
			"%w: the stored login for context %q expired",
			ErrNotLoggedIn, current.Name,
		)
	}
	if err != nil {
		return identity.Context{}, identity.Token{}, err
	}

	if renewed.Refresh == "" {
		renewed.Refresh = refresh.Value
	}
	newAccess, newRefresh := tokenPair(renewed, now)
	if newRefresh.ExpiresAt.IsZero() {
		newRefresh.ExpiresAt = refresh.ExpiresAt
	}
	if err := s.tokens.Save(
		current.KeychainSlot, newAccess, newRefresh,
	); err != nil {
		return identity.Context{}, identity.Token{}, err
	}
	return current, newAccess, nil
}

// SetPrincipal records the principal the core reported for a context, so
// whoami's answer survives into the next command.
func (s *Store) SetPrincipal(name, principal string) error {
	return s.contexts.Update(func(c *identity.Contexts) error {
		ctx, ok := c.Find(name)
		if !ok {
			return fmt.Errorf("auth: unknown context %q", name)
		}
		if ctx.Principal == principal {
			return nil
		}
		ctx.Principal = principal
		c.Put(ctx)
		return nil
	})
}

// Logout drops the current context: its tokens are deleted from the keychain
// and the context itself is removed, which clears current_context. A context
// with no tokens is not a login anyone can resume, so it is not kept around.
// It returns the name that was removed, or ErrNotLoggedIn when there was none.
func (s *Store) Logout() (string, error) {
	current, err := s.Current()
	if err != nil {
		return "", err
	}
	if err := s.tokens.Delete(current.KeychainSlot); err != nil {
		return "", err
	}
	err = s.contexts.Update(func(c *identity.Contexts) error {
		c.Remove(current.Name)
		return nil
	})
	if err != nil {
		return "", err
	}
	return current.Name, nil
}

// tokenPair turns what the core handed back into stored tokens. An access
// token with no stated lifetime keeps a zero expiry, which identity treats as
// expired and so refreshes before first use rather than trusting it.
func tokenPair(t core.Tokens, now time.Time) (access, refresh identity.Token) {
	access = identity.Token{Value: t.Access}
	if t.ExpiresIn > 0 {
		access.ExpiresAt = now.Add(t.ExpiresIn)
	}
	return access, identity.Token{Value: t.Refresh}
}
