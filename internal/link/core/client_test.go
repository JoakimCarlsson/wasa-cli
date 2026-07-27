package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNormalizeURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{in: "http://localhost:8080", want: "http://localhost:8080", ok: true},
		{in: "http://localhost:8080/", want: "http://localhost:8080", ok: true},
		{
			in:   "https://api.example.com/wasa/",
			want: "https://api.example.com/wasa",
			ok:   true,
		},
		{
			in:   " https://api.example.com ",
			want: "https://api.example.com",
			ok:   true,
		},
		{in: ""},
		{in: "example.com"},
		{in: "ftp://example.com"},
		{in: "https://"},
	}
	for _, c := range cases {
		got, err := NormalizeURL(c.in)
		if c.ok && err != nil {
			t.Fatalf("NormalizeURL(%q) error: %v", c.in, err)
		}
		if !c.ok {
			if err == nil {
				t.Fatalf("NormalizeURL(%q) = %q, want an error", c.in, got)
			}
			continue
		}
		if got != c.want {
			t.Fatalf("NormalizeURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLoginURL(t *testing.T) {
	c, err := New("https://api.example.com/")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	raw := c.LoginURL("github", "http://127.0.0.1:5151/callback", "st4te")

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	if u.Path != "/api/v1/auth/github/login" {
		t.Fatalf("path = %q", u.Path)
	}
	if got := u.Query().Get("redirect_uri"); got !=
		"http://127.0.0.1:5151/callback" {
		t.Fatalf("redirect_uri = %q", got)
	}
	if got := u.Query().Get("state"); got != "st4te" {
		t.Fatalf("state = %q", got)
	}
}

func TestHost(t *testing.T) {
	c, err := New("http://localhost:8080/base")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.Host(); got != "localhost:8080" {
		t.Fatalf("Host = %q, want localhost:8080", got)
	}
}

func TestRefresh(t *testing.T) {
	var got struct {
		RefreshToken string `json:"refresh_token"`
	}
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/auth/refresh" ||
				r.Method != http.MethodPost {
				t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			}
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Errorf("decode body: %v", err)
			}
			writeJSON(w, http.StatusOK, tokenResponse{
				AccessToken: "fresh-jwt",
				TokenType:   "Bearer",
				ExpiresIn:   900,
			})
		}))
	defer srv.Close()

	c := clientFor(t, srv.URL)
	tokens, err := c.Refresh(t.Context(), "the-refresh-token")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got.RefreshToken != "the-refresh-token" {
		t.Fatalf("sent refresh_token = %q", got.RefreshToken)
	}
	if tokens.Access != "fresh-jwt" {
		t.Fatalf("access = %q", tokens.Access)
	}
	if tokens.ExpiresIn != 15*time.Minute {
		t.Fatalf("expiresIn = %s, want 15m", tokens.ExpiresIn)
	}
}

func TestRefreshRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusUnauthorized, problemDetails{
				Title:  "Unauthorized",
				Status: http.StatusUnauthorized,
				Detail: "the refresh token is invalid, expired, or revoked",
			})
		}))
	defer srv.Close()

	_, err := clientFor(t, srv.URL).Refresh(t.Context(), "stale")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("err = %v, want the core's detail", err)
	}
}

func TestMe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer jwt" {
				t.Errorf("Authorization = %q", got)
			}
			writeJSON(w, http.StatusOK, meResponse{
				UserID: "01JABC", Handle: "octocat",
			})
		}))
	defer srv.Close()

	p, err := clientFor(t, srv.URL).Me(t.Context(), "jwt")
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if p.UserID != "01JABC" || p.Handle != "octocat" {
		t.Fatalf("principal = %+v", p)
	}
	if got := p.String(); got != "octocat (01JABC)" {
		t.Fatalf("String = %q", got)
	}
}

func TestPrincipalStringWithoutHandle(t *testing.T) {
	if got := (Principal{UserID: "01JABC"}).String(); got != "01JABC" {
		t.Fatalf("String = %q, want 01JABC", got)
	}
}

func TestServerErrorCarriesDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusBadGateway, problemDetails{
				Title:  "Bad Gateway",
				Status: http.StatusBadGateway,
				Detail: "the login provider could not be reached",
			})
		}))
	defer srv.Close()

	_, err := clientFor(t, srv.URL).Me(t.Context(), "jwt")
	if err == nil || !strings.Contains(err.Error(), "could not be reached") {
		t.Fatalf("err = %v", err)
	}
	if errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a 502 must not read as unauthorized: %v", err)
	}
}

func TestUnreachableCore(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) {}))
	base := srv.URL
	srv.Close()

	_, err := clientFor(t, base).Me(context.Background(), "jwt")
	if err == nil {
		t.Fatal("Me against a closed core = nil error")
	}
}

func clientFor(t *testing.T, base string) *Client {
	t.Helper()
	c, err := New(base)
	if err != nil {
		t.Fatalf("New(%q): %v", base, err)
	}
	return c
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
