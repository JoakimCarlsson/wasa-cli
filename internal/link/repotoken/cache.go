//go:build !windows

package repotoken

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/joakimcarlsson/wasa-cli/internal/link/core"
	"github.com/joakimcarlsson/wasa-cli/internal/link/identity"
)

// Action is the single git operation a minted token permits. The core mints
// one action per token, so a fetch credential can never push.
type Action string

// The actions a repo-scoped token can carry.
const (
	Pull Action = "pull"
	Push Action = "push"
)

// Exchanger is the one call the cache needs from a core: *core.Client
// satisfies it, and a test substitutes its own.
type Exchanger interface {
	Exchange(
		ctx context.Context,
		loginJWT, audience, scope string,
	) (core.ScopedToken, error)
}

// LoginJWTProvider hands back a login JWT that is valid now, refreshing it
// against the core first when the stored one is close to expiry.
//
// The cache holds the provider rather than a token so a long-lived process —
// a TUI, or a push that waits on a large transfer — always exchanges with the
// account credential as it stands at that moment, never a copy captured when
// the cache was built.
type LoginJWTProvider func(ctx context.Context) (string, error)

// Cache mints repo-scoped tokens and reuses them until they near expiry.
type Cache struct {
	core  Exchanger
	login LoginJWTProvider
	now   func() time.Time

	mu      sync.Mutex
	entries map[key]*entry
}

// key is what a cached token is good for. The audience is kept in the path
// form the core parses, so the two shapes of the same repo are separate
// entries — each is a distinct grant of authority and neither is derivable
// from the other without a round-trip.
type key struct {
	audience string
	action   Action
}

// entry is one cache slot. Its mutex covers the exchange, so concurrent
// callers for the same key wait for the first one's result instead of each
// making a round-trip; different keys hold different entries and so exchange
// in parallel.
//
// goodUntil is expiry less the margin, and is zero for a slot that holds
// nothing or holds a token the core stated no lifetime for — both mean
// "exchange".
type entry struct {
	mu        sync.Mutex
	token     string
	goodUntil time.Time
}

// margin is how long before expiry a cached token stops being handed out, so a
// git transfer never starts with a token that dies mid-flight.
//
// The login token store's identity.RefreshBuffer is the ceiling, not the
// answer: a repo-scoped token lives minutes where a login JWT lives a quarter
// of an hour, and a five-minute margin on a five-minute token would discard
// every token the moment it arrived. For anything that short the margin is a
// quarter of the stated lifetime instead.
func margin(lifetime time.Duration) time.Duration {
	if quarter := lifetime / 4; quarter < identity.RefreshBuffer {
		return quarter
	}
	return identity.RefreshBuffer
}

// NewCache returns a cache that exchanges against ex, using login for the
// parent credential of every exchange. Both are required.
func NewCache(ex Exchanger, login LoginJWTProvider) *Cache {
	return &Cache{
		core:    ex,
		login:   login,
		now:     time.Now,
		entries: make(map[key]*entry),
	}
}

// Token returns a token that authorizes action on the repo the audience names,
// exchanging for a fresh one when nothing usable is cached. Build the audience
// with core.RepoAudience or core.SlugAudience.
//
// A cached token is handed back until it comes within the margin of expiry, so
// a token is never handed to a git transfer that could outlive it. A token the
// core stated no lifetime for is used once and never cached.
func (c *Cache) Token(
	ctx context.Context,
	audience string,
	action Action,
) (string, error) {
	if audience == "" {
		return "", errors.New("repotoken: empty audience")
	}
	switch action {
	case Pull, Push:
	default:
		return "", errors.New("repotoken: action must be pull or push")
	}
	if c.core == nil || c.login == nil {
		return "", errors.New("repotoken: cache is not configured")
	}

	e := c.entryFor(key{audience: audience, action: action})

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.goodUntil.After(c.now()) {
		return e.token, nil
	}

	loginJWT, err := c.login(ctx)
	if err != nil {
		return "", err
	}
	scoped, err := c.core.Exchange(ctx, loginJWT, audience, string(action))
	if err != nil {
		return "", err
	}
	if scoped.Value == "" {
		return "", errors.New("repotoken: the core minted an empty token")
	}

	e.token = scoped.Value
	e.goodUntil = time.Time{}
	if scoped.ExpiresIn > 0 {
		e.goodUntil = c.now().
			Add(scoped.ExpiresIn - margin(scoped.ExpiresIn))
	}
	return e.token, nil
}

// Forget drops the cached token for one audience and action, so the next Token
// exchanges again. A caller that had a token rejected uses it rather than
// waiting out the cached lifetime.
func (c *Cache) Forget(audience string, action Action) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key{audience: audience, action: action})
}

// entryFor finds or creates the slot for a key. The map lock is held only for
// that lookup: the exchange itself runs under the entry's own lock, so one
// repo's round-trip never blocks another's.
func (c *Cache) entryFor(k key) *entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[k]
	if !ok {
		e = &entry{}
		c.entries[k] = e
	}
	return e
}
