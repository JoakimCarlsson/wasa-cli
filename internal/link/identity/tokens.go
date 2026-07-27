//go:build !windows

package identity

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

// RefreshBuffer is the safety margin applied to every token expiry. A token
// that dies within the buffer already counts as needing refresh, so a slow
// call never starts with a token that expires mid-flight.
const RefreshBuffer = 5 * time.Minute

// KeyringService is the service name every wasa secret is stored under.
const KeyringService = "wasa"

// ErrNoToken is returned when a slot holds no token for the requested kind.
var ErrNoToken = errors.New("identity: no token stored")

// Token is a bearer token with the expiry it was issued with. A zero ExpiresAt
// means unknown, which is treated as expired.
type Token struct {
	Value     string
	ExpiresAt time.Time
}

// NeedsRefresh reports whether the token must be refreshed before use at now.
// An empty value, an unknown expiry, or an expiry inside RefreshBuffer all
// count — the fail-safe answer is always "refresh".
func (t Token) NeedsRefresh(now time.Time) bool {
	if t.Value == "" || t.ExpiresAt.IsZero() {
		return true
	}
	return !t.ExpiresAt.After(now.Add(RefreshBuffer))
}

// encode renders the token as `value|expires_at_unix`. An unknown expiry
// encodes as 0, which decodes back to unknown and so to expired.
func (t Token) encode() string {
	var unix int64
	if !t.ExpiresAt.IsZero() {
		unix = t.ExpiresAt.Unix()
	}
	return t.Value + "|" + strconv.FormatInt(unix, 10)
}

// decodeToken parses `value|expires_at_unix`. A missing or unparseable suffix
// yields an unknown expiry rather than an error, so a token written by an
// older or broken writer is refreshed instead of trusted.
func decodeToken(s string) Token {
	value, expiry, ok := strings.Cut(s, "|")
	if !ok {
		return Token{Value: s}
	}
	unix, err := strconv.ParseInt(expiry, 10, 64)
	if err != nil || unix <= 0 {
		return Token{Value: value}
	}
	return Token{Value: value, ExpiresAt: time.Unix(unix, 0)}
}

// Keyring is the subset of an OS keychain the token store needs. The default
// implementation is the platform keychain; tests substitute their own.
type Keyring interface {
	Get(service, user string) (string, error)
	Set(service, user, password string) error
	Delete(service, user string) error
}

type systemKeyring struct{}

func (systemKeyring) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}

func (systemKeyring) Set(service, user, password string) error {
	return keyring.Set(service, user, password)
}

func (systemKeyring) Delete(service, user string) error {
	return keyring.Delete(service, user)
}

// TokenStore persists the access and refresh token of each context in the OS
// keychain, falling back to a 0600 JSON file when no keychain is available.
type TokenStore struct {
	keyring  Keyring
	fallback Keyring
}

// NewTokenStore returns a store over the platform keychain with the file
// fallback in wasa's config directory.
func NewTokenStore() (*TokenStore, error) {
	fallback, err := DefaultFileKeyring()
	if err != nil {
		return nil, err
	}
	return &TokenStore{keyring: systemKeyring{}, fallback: fallback}, nil
}

// NewTokenStoreWith returns a store over an explicit keychain and fallback.
// Either may be nil: a nil keychain forces the fallback, a nil fallback makes
// keychain failures fatal.
func NewTokenStoreWith(kr, fallback Keyring) *TokenStore {
	return &TokenStore{keyring: kr, fallback: fallback}
}

const (
	accessSuffix  = "/access"
	refreshSuffix = "/refresh"
)

// Save writes both tokens for a context's keychain slot.
func (s *TokenStore) Save(slot string, access, refresh Token) error {
	if slot == "" {
		return errors.New("identity: empty keychain slot")
	}
	if err := s.set(slot+accessSuffix, access); err != nil {
		return err
	}
	return s.set(slot+refreshSuffix, refresh)
}

// Load reads both tokens for a context's keychain slot. A slot with no access
// token reports ErrNoToken; a missing refresh token is not an error, since not
// every issuer hands one out.
func (s *TokenStore) Load(slot string) (access, refresh Token, err error) {
	if slot == "" {
		return Token{}, Token{}, errors.New("identity: empty keychain slot")
	}
	access, err = s.get(slot + accessSuffix)
	if err != nil {
		return Token{}, Token{}, err
	}
	refresh, err = s.get(slot + refreshSuffix)
	if err != nil && !errors.Is(err, ErrNoToken) {
		return Token{}, Token{}, err
	}
	return access, refresh, nil
}

// Delete removes both tokens for a context's keychain slot. A slot that holds
// nothing is not an error.
func (s *TokenStore) Delete(slot string) error {
	if slot == "" {
		return errors.New("identity: empty keychain slot")
	}
	for _, user := range []string{slot + accessSuffix, slot + refreshSuffix} {
		if err := s.delete(user); err != nil {
			return err
		}
	}
	return nil
}

func (s *TokenStore) set(user string, t Token) error {
	encoded := t.encode()
	if s.keyring != nil {
		err := s.keyring.Set(KeyringService, user, encoded)
		if err == nil {
			return nil
		}
		if s.fallback == nil {
			return fmt.Errorf("identity: keychain write: %w", err)
		}
	}
	if s.fallback == nil {
		return errors.New("identity: no keychain available")
	}
	return s.fallback.Set(KeyringService, user, encoded)
}

func (s *TokenStore) get(user string) (Token, error) {
	if s.keyring != nil {
		encoded, err := s.keyring.Get(KeyringService, user)
		switch {
		case err == nil:
			return decodeToken(encoded), nil
		case errors.Is(err, keyring.ErrNotFound):
			if s.fallback == nil {
				return Token{}, ErrNoToken
			}
		case s.fallback == nil:
			return Token{}, fmt.Errorf("identity: keychain read: %w", err)
		}
	}
	if s.fallback == nil {
		return Token{}, ErrNoToken
	}
	encoded, err := s.fallback.Get(KeyringService, user)
	if errors.Is(err, keyring.ErrNotFound) {
		return Token{}, ErrNoToken
	}
	if err != nil {
		return Token{}, err
	}
	return decodeToken(encoded), nil
}

func (s *TokenStore) delete(user string) error {
	if s.keyring != nil {
		err := s.keyring.Delete(KeyringService, user)
		switch {
		case err == nil, errors.Is(err, keyring.ErrNotFound):
		case s.fallback == nil:
			return fmt.Errorf("identity: keychain delete: %w", err)
		}
	}
	if s.fallback == nil {
		return nil
	}
	err := s.fallback.Delete(KeyringService, user)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return nil
}
