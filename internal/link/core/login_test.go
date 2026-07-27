package core

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// stubCore answers the login endpoint the way a wasa-api core does for a CLI:
// it drives the provider exchange itself and redirects the browser back to the
// loopback address with the tokens it minted.
func stubCore(t *testing.T, redirect func(url.Values) url.Values) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/auth/github/login" {
				http.NotFound(w, r)
				return
			}
			q := r.URL.Query()
			target, err := url.Parse(q.Get("redirect_uri"))
			if err != nil {
				t.Errorf("bad redirect_uri %q: %v", q.Get("redirect_uri"), err)
				return
			}
			target.RawQuery = redirect(q).Encode()
			http.Redirect(w, r, target.String(), http.StatusFound)
		}))
	t.Cleanup(srv.Close)
	return clientFor(t, srv.URL)
}

// browser follows the login URL the way a real browser would, redirects and
// all, so the loopback listener sees exactly the request it will see in
// production.
func browser(t *testing.T) func(string) error {
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

func TestLoginRoundTrip(t *testing.T) {
	client := stubCore(t, func(q url.Values) url.Values {
		return url.Values{
			"access_token":  {"login-jwt"},
			"refresh_token": {"refresh-token"},
			"expires_in":    {"900"},
			"state":         {q.Get("state")},
		}
	})

	var prompt bytes.Buffer
	tokens, err := Login(t.Context(), client, LoginOptions{
		Prompt:      &prompt,
		OpenBrowser: browser(t),
		Timeout:     10 * time.Second,
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if tokens.Access != "login-jwt" || tokens.Refresh != "refresh-token" {
		t.Fatalf("tokens = %+v", tokens)
	}
	if tokens.ExpiresIn != 15*time.Minute {
		t.Fatalf("expiresIn = %s, want 15m", tokens.ExpiresIn)
	}
	if !strings.Contains(prompt.String(), "/api/v1/auth/github/login") {
		t.Fatalf("prompt = %q, want the login URL", prompt.String())
	}
}

func TestLoginRejectsMismatchedState(t *testing.T) {
	client := stubCore(t, func(url.Values) url.Values {
		return url.Values{
			"access_token": {"login-jwt"},
			"state":        {"not-our-state"},
		}
	})

	_, err := Login(t.Context(), client, LoginOptions{
		OpenBrowser: browser(t),
		Timeout:     10 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "state does not match") {
		t.Fatalf("err = %v, want a state mismatch", err)
	}
}

func TestLoginReportsCoreError(t *testing.T) {
	client := stubCore(t, func(q url.Values) url.Values {
		return url.Values{
			"error":             {"access_denied"},
			"error_description": {"the provider rejected the code"},
			"state":             {q.Get("state")},
		}
	})

	_, err := Login(t.Context(), client, LoginOptions{
		OpenBrowser: browser(t),
		Timeout:     10 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "rejected the code") {
		t.Fatalf("err = %v, want the core's error description", err)
	}
}

func TestLoginRejectsTokenlessCallback(t *testing.T) {
	client := stubCore(t, func(q url.Values) url.Values {
		return url.Values{"state": {q.Get("state")}}
	})

	_, err := Login(t.Context(), client, LoginOptions{
		OpenBrowser: browser(t),
		Timeout:     10 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "no access token") {
		t.Fatalf("err = %v, want a missing-token error", err)
	}
}

func TestLoginTimesOut(t *testing.T) {
	client := stubCore(t, func(q url.Values) url.Values { return q })

	var prompt bytes.Buffer
	_, err := Login(t.Context(), client, LoginOptions{
		Prompt:      &prompt,
		OpenBrowser: func(string) error { return errors.New("no browser") },
		Timeout:     50 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if !strings.Contains(prompt.String(), "could not open a browser") {
		t.Fatalf("prompt = %q, want the manual-open hint", prompt.String())
	}
}

func TestLoginHonoursCancellation(t *testing.T) {
	client := stubCore(t, func(q url.Values) url.Values { return q })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Login(ctx, client, LoginOptions{
		OpenBrowser: func(string) error { return nil },
		Timeout:     10 * time.Second,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
