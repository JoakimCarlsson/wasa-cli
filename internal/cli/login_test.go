package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joakimcarlsson/wasa-cli/internal/link/auth"
	"github.com/joakimcarlsson/wasa-cli/internal/link/core"
	"github.com/joakimcarlsson/wasa-cli/internal/link/identity"
	"github.com/zalando/go-keyring"
)

// memKeyring is an in-memory keychain so the login commands never touch the
// host's real one under test.
type memKeyring struct{ secrets map[string]string }

func (m memKeyring) Get(service, user string) (string, error) {
	secret, ok := m.secrets[service+"\x00"+user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return secret, nil
}

func (m memKeyring) Set(service, user, password string) error {
	m.secrets[service+"\x00"+user] = password
	return nil
}

func (m memKeyring) Delete(service, user string) error {
	key := service + "\x00" + user
	if _, ok := m.secrets[key]; !ok {
		return keyring.ErrNotFound
	}
	delete(m.secrets, key)
	return nil
}

func testStore(t *testing.T) *auth.Store {
	t.Helper()
	return auth.NewStore(
		identity.NewContextStore(
			filepath.Join(t.TempDir(), identity.ContextsFileName),
		),
		identity.NewTokenStoreWith(
			memKeyring{secrets: map[string]string{}},
			nil,
		),
	)
}

// fakeCore is a wasa-api core reduced to the three routes login, whoami and
// the refresh-ahead path use.
func fakeCore(t *testing.T, handle string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/auth/github/login",
		func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			target, err := url.Parse(q.Get("redirect_uri"))
			if err != nil {
				t.Errorf("redirect_uri: %v", err)
				return
			}
			target.RawQuery = url.Values{
				"access_token":  {"login-jwt"},
				"refresh_token": {"refresh-token"},
				"expires_in":    {"900"},
				"state":         {q.Get("state")},
			}.Encode()
			http.Redirect(w, r, target.String(), http.StatusFound)
		})
	mux.HandleFunc("/api/v1/auth/me",
		func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(
				r.Header.Get("Authorization"), "Bearer ",
			) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"user_id": "01JABC", "handle": handle,
			})
		})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func openWith(t *testing.T) func(string) error {
	t.Helper()
	return func(raw string) error {
		go func() {
			resp, err := http.Get(raw)
			if err != nil {
				t.Errorf("browser: %v", err)
				return
			}
			resp.Body.Close()
		}()
		return nil
	}
}

func TestLoginWhoamiLogout(t *testing.T) {
	srv := fakeCore(t, "octocat")
	client, err := core.New(srv.URL)
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	store := testStore(t)

	var out bytes.Buffer
	err = login(t.Context(), client, store, "", core.LoginOptions{
		OpenBrowser: openWith(t),
	}, &out)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.Contains(out.String(), "logged in to "+srv.URL) ||
		!strings.Contains(out.String(), "octocat (01JABC)") {
		t.Fatalf("login output = %q", out.String())
	}

	out.Reset()
	if err := whoami(t.Context(), store, &out); err != nil {
		t.Fatalf("whoami: %v", err)
	}
	for _, want := range []string{
		"context:   " + client.Host(),
		"core:      " + srv.URL,
		"principal: octocat (01JABC)",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("whoami output = %q, want %q", out.String(), want)
		}
	}

	if _, err := store.Logout(); err != nil {
		t.Fatalf("logout: %v", err)
	}
	out.Reset()
	err = whoami(t.Context(), store, &out)
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("whoami after logout = %v, want not logged in", err)
	}
}

func TestWhoamiWithoutLogin(t *testing.T) {
	var out bytes.Buffer
	err := whoami(t.Context(), testStore(t), &out)
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("err = %v, want not logged in", err)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
}

func TestWhoamiRecordsARenamedPrincipal(t *testing.T) {
	srv := fakeCore(t, "renamed")
	client, err := core.New(srv.URL)
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	store := testStore(t)

	var out bytes.Buffer
	if err := login(t.Context(), client, store, "ctx", core.LoginOptions{
		OpenBrowser: openWith(t),
	}, &out); err != nil {
		t.Fatalf("login: %v", err)
	}
	if err := store.SetPrincipal("ctx", "stale"); err != nil {
		t.Fatalf("SetPrincipal: %v", err)
	}

	out.Reset()
	if err := whoami(t.Context(), store, &out); err != nil {
		t.Fatalf("whoami: %v", err)
	}
	current, err := store.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if current.Principal != "renamed (01JABC)" {
		t.Fatalf("stored principal = %q", current.Principal)
	}
}

func TestLoginRequiresACore(t *testing.T) {
	t.Setenv(coreEnv, "")
	err := runLogin(nil)
	if err == nil || !strings.Contains(err.Error(), "no core to log in to") {
		t.Fatalf("err = %v, want a missing-core error", err)
	}
}

func TestLoginRejectsABadCoreURL(t *testing.T) {
	err := runLogin([]string{"--core", "not-a-url"})
	if err == nil || !strings.Contains(err.Error(), "must be http or https") {
		t.Fatalf("err = %v, want a URL error", err)
	}
}

func TestLoginCommandsAreRegistered(t *testing.T) {
	for _, name := range []string{"login", "logout", "whoami"} {
		if _, ok := lookup(name); !ok {
			t.Fatalf("%q is not registered", name)
		}
	}
}
