//go:build !windows

package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joakimcarlsson/wasa-cli/internal/link/core"
	"github.com/joakimcarlsson/wasa-cli/internal/link/identity"
	"github.com/zalando/go-keyring"
)

type fakeKeyring struct {
	secrets map[string]string
}

func newFakeKeyring() *fakeKeyring {
	return &fakeKeyring{secrets: map[string]string{}}
}

func (f *fakeKeyring) Get(service, user string) (string, error) {
	secret, ok := f.secrets[service+"\x00"+user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return secret, nil
}

func (f *fakeKeyring) Set(service, user, password string) error {
	f.secrets[service+"\x00"+user] = password
	return nil
}

func (f *fakeKeyring) Delete(service, user string) error {
	key := service + "\x00" + user
	if _, ok := f.secrets[key]; !ok {
		return keyring.ErrNotFound
	}
	delete(f.secrets, key)
	return nil
}

func newStore(t *testing.T) (*Store, *fakeKeyring) {
	t.Helper()
	kr := newFakeKeyring()
	contexts := identity.NewContextStore(
		filepath.Join(t.TempDir(), identity.ContextsFileName),
	)
	return NewStore(contexts, identity.NewTokenStoreWith(kr, nil)), kr
}

func TestSaveSetsCurrentContext(t *testing.T) {
	store, _ := newStore(t)
	now := time.Unix(1_700_000_000, 0)

	saved, err := store.Save(Login{
		Name:      "localhost:8080",
		CoreURL:   "http://localhost:8080",
		Principal: "octocat (01JABC)",
		Tokens: core.Tokens{
			Access:    "login-jwt",
			Refresh:   "refresh-token",
			ExpiresIn: 15 * time.Minute,
		},
	}, now)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.KeychainSlot != "localhost:8080" {
		t.Fatalf("slot = %q", saved.KeychainSlot)
	}

	current, err := store.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if current.Name != "localhost:8080" ||
		current.Principal != "octocat (01JABC)" {
		t.Fatalf("current = %+v", current)
	}

	ctx, access, err := store.Access(t.Context(), now)
	if err != nil {
		t.Fatalf("Access: %v", err)
	}
	if ctx.Name != current.Name {
		t.Fatalf("context = %+v", ctx)
	}
	if access.Value != "login-jwt" {
		t.Fatalf("access = %q", access.Value)
	}
	if want := now.Add(15 * time.Minute); !access.ExpiresAt.Equal(want) {
		t.Fatalf("expiresAt = %s, want %s", access.ExpiresAt, want)
	}
}

func TestSaveReplacesTokensForTheSameContext(t *testing.T) {
	store, _ := newStore(t)
	now := time.Unix(1_700_000_000, 0)
	login := Login{
		Name:    "localhost:8080",
		CoreURL: "http://localhost:8080",
		Tokens:  core.Tokens{Access: "first", ExpiresIn: time.Hour},
	}
	if _, err := store.Save(login, now); err != nil {
		t.Fatalf("Save: %v", err)
	}
	login.Tokens.Access = "second"
	if _, err := store.Save(login, now); err != nil {
		t.Fatalf("Save again: %v", err)
	}

	_, access, err := store.Access(t.Context(), now)
	if err != nil {
		t.Fatalf("Access: %v", err)
	}
	if access.Value != "second" {
		t.Fatalf("access = %q, want the newest login", access.Value)
	}
}

func TestAccessWithoutLogin(t *testing.T) {
	store, _ := newStore(t)

	if _, err := store.Current(); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("Current err = %v, want ErrNotLoggedIn", err)
	}
	_, _, err := store.Access(t.Context(), time.Now())
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("Access err = %v, want ErrNotLoggedIn", err)
	}
}

func TestAccessRefreshesNearExpiry(t *testing.T) {
	var sent struct {
		RefreshToken string `json:"refresh_token"`
	}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			calls++
			if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
				t.Errorf("decode: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "renewed-jwt",
				"token_type":   "Bearer",
				"expires_in":   900,
			})
		}))
	defer srv.Close()

	store, _ := newStore(t)
	now := time.Unix(1_700_000_000, 0)
	if _, err := store.Save(Login{
		Name:    "core",
		CoreURL: srv.URL,
		Tokens: core.Tokens{
			Access:    "old-jwt",
			Refresh:   "refresh-token",
			ExpiresIn: time.Minute,
		},
	}, now); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, access, err := store.Access(t.Context(), now)
	if err != nil {
		t.Fatalf("Access: %v", err)
	}
	if calls != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls)
	}
	if sent.RefreshToken != "refresh-token" {
		t.Fatalf("sent refresh_token = %q", sent.RefreshToken)
	}
	if access.Value != "renewed-jwt" {
		t.Fatalf("access = %q, want the renewed token", access.Value)
	}

	_, again, err := store.Access(t.Context(), now)
	if err != nil {
		t.Fatalf("Access again: %v", err)
	}
	if calls != 1 {
		t.Fatalf("refresh calls = %d, want the renewal to be persisted", calls)
	}
	if again.Value != "renewed-jwt" {
		t.Fatalf("access = %q on the second call", again.Value)
	}
}

func TestAccessKeepsRefreshTokenAcrossRenewal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "renewed-jwt",
				"expires_in":   1,
			})
		}))
	defer srv.Close()

	store, kr := newStore(t)
	now := time.Unix(1_700_000_000, 0)
	if _, err := store.Save(Login{
		Name:    "core",
		CoreURL: srv.URL,
		Tokens: core.Tokens{
			Access:  "old-jwt",
			Refresh: "refresh-token",
		},
	}, now); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, _, err := store.Access(t.Context(), now); err != nil {
		t.Fatalf("Access: %v", err)
	}

	got, err := kr.Get(identity.KeyringService, "core/refresh")
	if err != nil {
		t.Fatalf("refresh slot: %v", err)
	}
	if !strings.HasPrefix(got, "refresh-token|") {
		t.Fatalf("stored refresh = %q, want the original token kept", got)
	}
}

func TestAccessOnRejectedRefresh(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"title": "Unauthorized", "status": 401,
				"detail": "the refresh token is invalid, expired, or revoked",
			})
		}))
	defer srv.Close()

	store, _ := newStore(t)
	now := time.Unix(1_700_000_000, 0)
	if _, err := store.Save(Login{
		Name:    "core",
		CoreURL: srv.URL,
		Tokens:  core.Tokens{Access: "old-jwt", Refresh: "stale"},
	}, now); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, _, err := store.Access(t.Context(), now)
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("err = %v, want ErrNotLoggedIn", err)
	}
}

func TestLogout(t *testing.T) {
	store, kr := newStore(t)
	now := time.Unix(1_700_000_000, 0)
	if _, err := store.Save(Login{
		Name:    "localhost:8080",
		CoreURL: "http://localhost:8080",
		Tokens: core.Tokens{
			Access:    "login-jwt",
			Refresh:   "refresh-token",
			ExpiresIn: time.Hour,
		},
	}, now); err != nil {
		t.Fatalf("Save: %v", err)
	}

	name, err := store.Logout()
	if err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if name != "localhost:8080" {
		t.Fatalf("name = %q", name)
	}
	if len(kr.secrets) != 0 {
		t.Fatalf("keychain still holds %v", kr.secrets)
	}
	if _, err := store.Current(); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("Current after logout = %v, want ErrNotLoggedIn", err)
	}
	if _, err := store.Logout(); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("second Logout = %v, want ErrNotLoggedIn", err)
	}
}

func TestSetPrincipal(t *testing.T) {
	store, _ := newStore(t)
	now := time.Unix(1_700_000_000, 0)
	if _, err := store.Save(Login{
		Name:      "core",
		CoreURL:   "http://localhost:8080",
		Principal: "old",
		Tokens:    core.Tokens{Access: "jwt", ExpiresIn: time.Hour},
	}, now); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := store.SetPrincipal("core", "new (01JABC)"); err != nil {
		t.Fatalf("SetPrincipal: %v", err)
	}
	current, err := store.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if current.Principal != "new (01JABC)" {
		t.Fatalf("principal = %q", current.Principal)
	}
	if current.CoreURL != "http://localhost:8080" {
		t.Fatalf("core URL lost: %+v", current)
	}
	if err := store.SetPrincipal("nope", "x"); err == nil {
		t.Fatal("SetPrincipal on an unknown context = nil error")
	}
}
