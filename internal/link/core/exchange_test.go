package core

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestExchange(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/oauth/token" || r.Method != http.MethodPost {
				t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			}
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse form: %v", err)
			}
			form = r.PostForm
			writeJSON(w, http.StatusOK, exchangeBody{
				AccessToken:     "repo-scoped-jwt",
				IssuedTokenType: "urn:ietf:params:oauth:token-type:jwt",
				TokenType:       "Bearer",
				ExpiresIn:       300,
				Scope:           "push",
			})
		}))
	defer srv.Close()

	got, err := clientFor(t, srv.URL).Exchange(
		t.Context(), "login-jwt", "/et/acme/infra", "push",
	)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if form.Get("grant_type") != GrantTypeTokenExchange {
		t.Fatalf("grant_type = %q", form.Get("grant_type"))
	}
	if form.Get("subject_token") != "login-jwt" {
		t.Fatalf("subject_token = %q", form.Get("subject_token"))
	}
	if form.Get("audience") != "/et/acme/infra" {
		t.Fatalf("audience = %q", form.Get("audience"))
	}
	if form.Get("scope") != "push" {
		t.Fatalf("scope = %q", form.Get("scope"))
	}
	if got.Value != "repo-scoped-jwt" || got.Scope != "push" {
		t.Fatalf("token = %+v", got)
	}
	if got.ExpiresIn != 5*time.Minute {
		t.Fatalf("expiresIn = %s, want 5m", got.ExpiresIn)
	}
}

func TestExchangeDenied(t *testing.T) {
	cases := []struct {
		name   string
		status int
		detail string
		want   error
	}{
		{
			name:   "stale login",
			status: http.StatusUnauthorized,
			detail: "the subject token is expired",
			want:   ErrUnauthorized,
		},
		{
			name:   "no access",
			status: http.StatusForbidden,
			detail: "push is not permitted on this repo",
			want:   ErrForbidden,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					writeJSON(w, c.status, problemBody{
						Status: c.status,
						Detail: c.detail,
					})
				}))
			defer srv.Close()

			_, err := clientFor(t, srv.URL).Exchange(
				t.Context(), "login-jwt", "/et/acme/infra", "push",
			)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if !strings.Contains(err.Error(), c.detail) {
				t.Fatalf("err = %v, want the core's detail", err)
			}
		})
	}
}

func TestAudienceShapes(t *testing.T) {
	const repoID = "01K1AAAAAAAAAAAAAAAAAAAAAA"
	if got := RepoAudience(repoID + ".git"); got != "/git/repo/"+repoID {
		t.Fatalf("RepoAudience = %q", got)
	}
	if got := SlugAudience("acme", "infra.git"); got != "/et/acme/infra" {
		t.Fatalf("SlugAudience = %q", got)
	}
}

type exchangeBody struct {
	AccessToken     string `json:"access_token"`
	IssuedTokenType string `json:"issued_token_type"`
	TokenType       string `json:"token_type"`
	Scope           string `json:"scope"`
	ExpiresIn       int    `json:"expires_in"`
}
