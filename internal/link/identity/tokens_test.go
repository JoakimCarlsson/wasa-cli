//go:build !windows

package identity

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

// fakeKeyring is an in-memory keychain.
type fakeKeyring struct {
	secrets map[string]string
	fail    error
}

func newFakeKeyring() *fakeKeyring {
	return &fakeKeyring{secrets: map[string]string{}}
}

func (f *fakeKeyring) Get(service, user string) (string, error) {
	if f.fail != nil {
		return "", f.fail
	}
	secret, ok := f.secrets[service+"\x00"+user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return secret, nil
}

func (f *fakeKeyring) Set(service, user, password string) error {
	if f.fail != nil {
		return f.fail
	}
	f.secrets[service+"\x00"+user] = password
	return nil
}

func (f *fakeKeyring) Delete(service, user string) error {
	if f.fail != nil {
		return f.fail
	}
	key := service + "\x00" + user
	if _, ok := f.secrets[key]; !ok {
		return keyring.ErrNotFound
	}
	delete(f.secrets, key)
	return nil
}

func TestNeedsRefresh(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cases := []struct {
		name  string
		token Token
		want  bool
	}{
		{
			name:  "fresh",
			token: Token{Value: "t", ExpiresAt: now.Add(time.Hour)},
			want:  false,
		},
		{
			name:  "unknown expiry assumed expired",
			token: Token{Value: "t"},
			want:  true,
		},
		{
			name:  "empty value",
			token: Token{ExpiresAt: now.Add(time.Hour)},
			want:  true,
		},
		{
			name:  "already expired",
			token: Token{Value: "t", ExpiresAt: now.Add(-time.Second)},
			want:  true,
		},
		{
			name: "inside the buffer",
			token: Token{
				Value:     "t",
				ExpiresAt: now.Add(RefreshBuffer - time.Minute),
			},
			want: true,
		},
		{
			name:  "exactly at the buffer edge",
			token: Token{Value: "t", ExpiresAt: now.Add(RefreshBuffer)},
			want:  true,
		},
		{
			name: "just outside the buffer",
			token: Token{
				Value:     "t",
				ExpiresAt: now.Add(RefreshBuffer + time.Second),
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.token.NeedsRefresh(now); got != tc.want {
				t.Fatalf("NeedsRefresh = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTokenEncoding(t *testing.T) {
	expiry := time.Unix(1_700_000_000, 0)
	encoded := Token{Value: "abc", ExpiresAt: expiry}.encode()
	if encoded != "abc|1700000000" {
		t.Fatalf("encode = %q", encoded)
	}
	if got := decodeToken(encoded); got.Value != "abc" ||
		!got.ExpiresAt.Equal(expiry) {
		t.Fatalf("decode round-trip = %+v", got)
	}

	unknown := Token{Value: "abc"}
	if got := unknown.encode(); got != "abc|0" {
		t.Fatalf("encode of unknown expiry = %q", got)
	}
	for _, raw := range []string{"abc|0", "abc", "abc|nonsense", "abc|-5"} {
		got := decodeToken(raw)
		if got.Value != "abc" {
			t.Fatalf("decode(%q) value = %q", raw, got.Value)
		}
		if !got.ExpiresAt.IsZero() {
			t.Fatalf("decode(%q) invented an expiry %v", raw, got.ExpiresAt)
		}
		if !got.NeedsRefresh(time.Unix(0, 0)) {
			t.Fatalf("decode(%q) did not fail safe", raw)
		}
	}
}

func TestTokenStoreKeychainRoundTrip(t *testing.T) {
	kr := newFakeKeyring()
	fallback := NewFileKeyring(
		filepath.Join(t.TempDir(), CredentialsFileName), nil,
	)
	s := NewTokenStoreWith(kr, fallback)

	expiry := time.Unix(1_700_000_000, 0)
	access := Token{Value: "access-tok", ExpiresAt: expiry}
	refresh := Token{Value: "refresh-tok", ExpiresAt: expiry.Add(time.Hour)}
	if err := s.Save("personal", access, refresh); err != nil {
		t.Fatalf("Save: %v", err)
	}

	gotAccess, gotRefresh, err := s.Load("personal")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if gotAccess.Value != access.Value ||
		!gotAccess.ExpiresAt.Equal(access.ExpiresAt) {
		t.Fatalf("access = %+v, want %+v", gotAccess, access)
	}
	if gotRefresh.Value != refresh.Value {
		t.Fatalf("refresh = %+v, want %+v", gotRefresh, refresh)
	}
	if _, err := os.Stat(fallback.Path()); !os.IsNotExist(err) {
		t.Fatalf("fallback file was written while a keychain worked (%v)", err)
	}

	if err := s.Delete("personal"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := s.Load("personal"); !errors.Is(err, ErrNoToken) {
		t.Fatalf("Load after Delete = %v, want ErrNoToken", err)
	}
	if err := s.Delete("personal"); err != nil {
		t.Fatalf("Delete of an empty slot: %v", err)
	}
}

// TestTokenStoreFallsBackToFile covers a host with no keychain: the store must
// keep working through the JSON file.
func TestTokenStoreFallsBackToFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), CredentialsFileName)
	kr := newFakeKeyring()
	kr.fail = keyring.ErrUnsupportedPlatform
	s := NewTokenStoreWith(kr, NewFileKeyring(path, nil))

	expiry := time.Unix(1_700_000_000, 0)
	err := s.Save(
		"personal",
		Token{Value: "access-tok", ExpiresAt: expiry},
		Token{Value: "refresh-tok", ExpiresAt: expiry},
	)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fallback: %v", err)
	}
	if !strings.Contains(string(data), `"`+KeyringService+`"`) {
		t.Fatalf("fallback is not service-keyed: %s", data)
	}
	if !strings.Contains(string(data), "access-tok|1700000000") {
		t.Fatalf("fallback missing the encoded token: %s", data)
	}

	access, refresh, err := s.Load("personal")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if access.Value != "access-tok" || refresh.Value != "refresh-tok" {
		t.Fatalf("fallback round-trip = %+v / %+v", access, refresh)
	}
	if !access.ExpiresAt.Equal(expiry) {
		t.Fatalf("fallback lost the expiry: %v", access.ExpiresAt)
	}
	if err := s.Delete("personal"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := s.Load("personal"); !errors.Is(err, ErrNoToken) {
		t.Fatalf("Load after Delete = %v, want ErrNoToken", err)
	}
}

// TestTokenStoreMissingRefreshIsNotFatal covers an issuer that hands out only
// an access token.
func TestTokenStoreMissingRefreshIsNotFatal(t *testing.T) {
	kr := newFakeKeyring()
	s := NewTokenStoreWith(kr, nil)
	if err := kr.Set(KeyringService, "personal/access", "tok|0"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	access, refresh, err := s.Load("personal")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if access.Value != "tok" {
		t.Fatalf("access = %+v", access)
	}
	if refresh.Value != "" {
		t.Fatalf("invented a refresh token %+v", refresh)
	}
}

func TestTokenStoreRejectsEmptySlot(t *testing.T) {
	s := NewTokenStoreWith(newFakeKeyring(), nil)
	if err := s.Save("", Token{}, Token{}); err == nil {
		t.Fatal("Save accepted an empty slot")
	}
	if _, _, err := s.Load(""); err == nil {
		t.Fatal("Load accepted an empty slot")
	}
	if err := s.Delete(""); err == nil {
		t.Fatal("Delete accepted an empty slot")
	}
}

// TestFileKeyringWarnsOnLoosePerms checks a world-readable secret file is
// called out rather than silently trusted.
func TestFileKeyringWarnsOnLoosePerms(t *testing.T) {
	path := filepath.Join(t.TempDir(), CredentialsFileName)
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var warn bytes.Buffer
	k := NewFileKeyring(path, &warn)
	if _, err := k.Get(KeyringService, "personal/access"); !errors.Is(
		err, keyring.ErrNotFound,
	) {
		t.Fatalf("Get = %v, want ErrNotFound", err)
	}
	if !strings.Contains(warn.String(), "0644") {
		t.Fatalf("no permission warning: %q", warn.String())
	}

	warn.Reset()
	if err := k.Set(KeyringService, "personal/access", "tok|0"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	warn.Reset()
	if _, err := k.Get(KeyringService, "personal/access"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if warn.Len() != 0 {
		t.Fatalf("warned about a rewritten 0600 file: %q", warn.String())
	}
}

// TestDefaultStoresUseUserDirs checks the default constructors resolve through
// the one path resolver, which under test means the throwaway dir.
func TestDefaultStoresUseUserDirs(t *testing.T) {
	cs, err := DefaultContextStore()
	if err != nil {
		t.Fatalf("DefaultContextStore: %v", err)
	}
	fk, err := DefaultFileKeyring()
	if err != nil {
		t.Fatalf("DefaultFileKeyring: %v", err)
	}
	if filepath.Base(cs.Path()) != ContextsFileName {
		t.Fatalf("context store at %q", cs.Path())
	}
	if filepath.Base(fk.Path()) != CredentialsFileName {
		t.Fatalf("credentials at %q", fk.Path())
	}
	if filepath.Dir(cs.Path()) != filepath.Dir(fk.Path()) {
		t.Fatalf(
			"stores split across dirs: %q vs %q",
			cs.Path(), fk.Path(),
		)
	}
	if !strings.HasPrefix(cs.Path(), os.TempDir()) {
		t.Fatalf("default store %q escaped the test temp dir", cs.Path())
	}
	if _, err := NewTokenStore(); err != nil {
		t.Fatalf("NewTokenStore: %v", err)
	}
}
