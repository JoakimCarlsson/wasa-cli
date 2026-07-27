//go:build !windows

package repotoken

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joakimcarlsson/wasa-cli/internal/link/core"
	"github.com/joakimcarlsson/wasa-cli/internal/link/identity"
)

// stubCore answers exchanges without a network. gate, when set, blocks every
// exchange until it is closed, which is how a test observes how many are in
// flight at once.
type stubCore struct {
	calls atomic.Int64
	gate  chan struct{}
	ttl   time.Duration
	err   error

	mu      sync.Mutex
	audit   []string
	entered chan struct{}
}

func (s *stubCore) Exchange(
	ctx context.Context,
	loginJWT, audience, scope string,
) (core.ScopedToken, error) {
	s.calls.Add(1)
	s.mu.Lock()
	s.audit = append(
		s.audit, fmt.Sprintf("%s|%s|%s", loginJWT, audience, scope),
	)
	s.mu.Unlock()
	if s.entered != nil {
		s.entered <- struct{}{}
	}
	if s.gate != nil {
		select {
		case <-s.gate:
		case <-ctx.Done():
			return core.ScopedToken{}, ctx.Err()
		}
	}
	if s.err != nil {
		return core.ScopedToken{}, s.err
	}
	ttl := s.ttl
	if ttl == 0 {
		ttl = time.Hour
	}
	return core.ScopedToken{
		Value:     "scoped:" + audience + ":" + scope,
		Scope:     scope,
		ExpiresIn: ttl,
	}, nil
}

// sent returns the "<login jwt>|<audience>|<scope>" of every exchange, in
// order.
func (s *stubCore) sent() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.audit...)
}

func TestTokenCachesPerAudienceAndAction(t *testing.T) {
	stub := &stubCore{}
	cache := NewCache(stub, staticLogin("login-jwt"))
	const audience = "/git/repo/01K1AAAAAAAAAAAAAAAAAAAAAA"

	first, err := cache.Token(t.Context(), audience, Push)
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if first != "scoped:"+audience+":push" {
		t.Fatalf("token = %q", first)
	}
	again, err := cache.Token(t.Context(), audience, Push)
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if again != first {
		t.Fatalf("second token = %q, want the cached %q", again, first)
	}
	if got := stub.calls.Load(); got != 1 {
		t.Fatalf("exchanges = %d, want 1", got)
	}

	if _, err := cache.Token(t.Context(), audience, Pull); err != nil {
		t.Fatalf("Token(pull): %v", err)
	}
	if got := stub.calls.Load(); got != 2 {
		t.Fatalf("exchanges = %d, want 2: pull is its own grant", got)
	}

	slug := core.SlugAudience("acme", "infra")
	if _, err := cache.Token(t.Context(), slug, Push); err != nil {
		t.Fatalf("Token(slug): %v", err)
	}
	if got := stub.calls.Load(); got != 3 {
		t.Fatalf("exchanges = %d, want 3: the slug shape is its own key", got)
	}
}

func TestTokenDedupesConcurrentFetchesForOneKey(t *testing.T) {
	stub := &stubCore{
		gate:    make(chan struct{}),
		entered: make(chan struct{}, 1),
	}
	cache := NewCache(stub, staticLogin("login-jwt"))
	const audience = "/et/acme/infra"

	const callers = 8
	tokens := make([]string, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := cache.Token(context.Background(), audience, Push)
			if err != nil {
				t.Errorf("Token: %v", err)
				return
			}
			tokens[i] = got
		}()
	}

	<-stub.entered
	close(stub.gate)
	wg.Wait()

	if got := stub.calls.Load(); got != 1 {
		t.Fatalf("exchanges = %d, want 1 for %d concurrent callers",
			got, callers)
	}
	for i, got := range tokens {
		if got != tokens[0] {
			t.Fatalf("caller %d got %q, want %q", i, got, tokens[0])
		}
	}
}

// TestTokenExchangesRepositoriesInParallel holds every exchange open until all
// of them have started: a lock held across the round-trip would serialize them
// and time out here instead.
func TestTokenExchangesRepositoriesInParallel(t *testing.T) {
	const repos = 4
	stub := &stubCore{
		gate:    make(chan struct{}),
		entered: make(chan struct{}, repos),
	}
	cache := NewCache(stub, staticLogin("login-jwt"))

	var wg sync.WaitGroup
	for i := range repos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			audience := core.SlugAudience("acme", "repo"+strconv.Itoa(i))
			if _, err := cache.Token(
				context.Background(), audience, Pull,
			); err != nil {
				t.Errorf("Token: %v", err)
			}
		}()
	}

	deadline := time.After(5 * time.Second)
	for range repos {
		select {
		case <-stub.entered:
		case <-deadline:
			t.Fatalf("only some exchanges ran in parallel: %d of %d",
				stub.calls.Load(), repos)
		}
	}
	close(stub.gate)
	wg.Wait()
}

// TestTokenIsCachedForMostOfItsLife pins the margin: a five-minute token — the
// core's default — is reused for most of its life and replaced before it dies,
// which the login store's own five-minute buffer would make impossible.
func TestTokenIsCachedForMostOfItsLife(t *testing.T) {
	const ttl = 5 * time.Minute
	stub := &stubCore{ttl: ttl}
	cache := NewCache(stub, staticLogin("login-jwt"))
	clock := newClock(cache)
	const audience = "/et/acme/infra"

	if _, err := cache.Token(t.Context(), audience, Push); err != nil {
		t.Fatalf("Token: %v", err)
	}
	clock.advance(ttl - margin(ttl) - time.Second)
	if _, err := cache.Token(t.Context(), audience, Push); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got := stub.calls.Load(); got != 1 {
		t.Fatalf("exchanges = %d, want 1 while the token is still good", got)
	}

	clock.advance(2 * time.Second)
	if _, err := cache.Token(t.Context(), audience, Push); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got := stub.calls.Load(); got != 2 {
		t.Fatalf("exchanges = %d, want 2 once inside the %s margin",
			got, margin(ttl))
	}
	if margin(ttl) >= ttl {
		t.Fatalf("margin %s consumes the whole %s lifetime", margin(ttl), ttl)
	}
	if margin(time.Hour) != identity.RefreshBuffer {
		t.Fatalf("margin(1h) = %s, want the login store's %s",
			margin(time.Hour), identity.RefreshBuffer)
	}
}

// TestTokenRefreshesTheLoginJWTBeforeExchanging pins that the replacement
// exchange uses the login JWT the provider hands back at that moment — the
// account credential as it stands, not the copy the first exchange used.
func TestTokenRefreshesTheLoginJWTBeforeExchanging(t *testing.T) {
	const ttl = 5 * time.Minute
	stub := &stubCore{ttl: ttl}
	var issued atomic.Int64
	cache := NewCache(stub, func(context.Context) (string, error) {
		return "login-jwt-" + strconv.FormatInt(issued.Add(1), 10), nil
	})
	clock := newClock(cache)
	const audience = "/et/acme/infra"

	if _, err := cache.Token(t.Context(), audience, Push); err != nil {
		t.Fatalf("Token: %v", err)
	}
	clock.advance(ttl)
	if _, err := cache.Token(t.Context(), audience, Push); err != nil {
		t.Fatalf("Token: %v", err)
	}
	sent := stub.sent()
	want := []string{
		"login-jwt-1|" + audience + "|push",
		"login-jwt-2|" + audience + "|push",
	}
	if len(sent) != len(want) {
		t.Fatalf("exchanges = %d, want %d", len(sent), len(want))
	}
	for i, w := range want {
		if sent[i] != w {
			t.Fatalf("exchange %d sent %q, want %q", i, sent[i], w)
		}
	}
}

// TestTokenWithoutAStatedLifetimeIsNotCached: a core that states no expiry
// leaves nothing to reason about, so the fail-safe answer is to exchange.
func TestTokenWithoutAStatedLifetimeIsNotCached(t *testing.T) {
	stub := &stubCore{ttl: -1}
	cache := NewCache(stub, staticLogin("login-jwt"))

	for range 2 {
		if _, err := cache.Token(t.Context(), "/et/acme/infra", Pull); err != nil {
			t.Fatalf("Token: %v", err)
		}
	}
	if got := stub.calls.Load(); got != 2 {
		t.Fatalf("exchanges = %d, want 2", got)
	}
}

func TestTokenReportsADeadLogin(t *testing.T) {
	stub := &stubCore{}
	want := errors.New("not logged in")
	cache := NewCache(stub, func(context.Context) (string, error) {
		return "", want
	})

	_, err := cache.Token(t.Context(), "/et/acme/infra", Push)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if got := stub.calls.Load(); got != 0 {
		t.Fatalf("exchanges = %d, want 0 without a login JWT", got)
	}
}

// TestTokenSurfacesTheCoresRefusal also pins that a refusal is not cached:
// access may be granted between two attempts.
func TestTokenSurfacesTheCoresRefusal(t *testing.T) {
	stub := &stubCore{err: core.ErrForbidden}
	cache := NewCache(stub, staticLogin("login-jwt"))

	_, err := cache.Token(t.Context(), "/et/acme/infra", Push)
	if !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if _, err := cache.Token(t.Context(), "/et/acme/infra", Push); err == nil {
		t.Fatal("second Token = nil error")
	}
	if got := stub.calls.Load(); got != 2 {
		t.Fatalf("exchanges = %d, want 2", got)
	}
}

func TestForgetDropsACachedToken(t *testing.T) {
	stub := &stubCore{}
	cache := NewCache(stub, staticLogin("login-jwt"))
	const audience = "/et/acme/infra"

	if _, err := cache.Token(t.Context(), audience, Push); err != nil {
		t.Fatalf("Token: %v", err)
	}
	cache.Forget(audience, Push)
	if _, err := cache.Token(t.Context(), audience, Push); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got := stub.calls.Load(); got != 2 {
		t.Fatalf("exchanges = %d, want 2 after Forget", got)
	}
}

func TestTokenRejectsBadInput(t *testing.T) {
	cache := NewCache(&stubCore{}, staticLogin("login-jwt"))
	if _, err := cache.Token(t.Context(), "", Push); err == nil {
		t.Fatal("an empty audience = nil error")
	}
	if _, err := cache.Token(
		t.Context(), "/et/acme/infra", Action("admin"),
	); err == nil {
		t.Fatal("an unknown action = nil error")
	}
}

// clock replaces a cache's time source so a test can walk a token to the edge
// of its life without waiting for it.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock(c *Cache) *clock {
	k := &clock{now: time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)}
	c.now = k.read
	return k
}

func (k *clock) read() time.Time {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.now
}

func (k *clock) advance(d time.Duration) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.now = k.now.Add(d)
}

func staticLogin(jwt string) LoginJWTProvider {
	return func(context.Context) (string, error) { return jwt, nil }
}
