package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultProvider is the login provider used when none is named. The core
// selects its provider by path segment, so a second one drops in without a
// change here.
const DefaultProvider = "github"

// ErrUnauthorized reports that the core rejected the credential presented —
// an expired login JWT, or a refresh token that is unknown, expired or
// revoked. Callers turn it into "log in again" rather than a transport error.
var ErrUnauthorized = errors.New("core: unauthorized")

// Tokens is the credential pair a completed login or refresh yields. Refresh
// is empty on a renewal: the core reissues only the login JWT and expects the
// caller to keep the refresh token it already holds.
type Tokens struct {
	Access    string
	Refresh   string
	ExpiresIn time.Duration
}

// Principal is the identity a login JWT names, as the core reports it.
type Principal struct {
	UserID string
	Handle string
}

// String renders the principal for humans: the handle with its user id when
// the core knows one, the bare id otherwise.
func (p Principal) String() string {
	if p.Handle == "" {
		return p.UserID
	}
	return fmt.Sprintf("%s (%s)", p.Handle, p.UserID)
}

// Client calls one wasa-api core.
type Client struct {
	baseURL string
	http    *http.Client
}

// DefaultTimeout bounds every non-interactive call to a core.
const DefaultTimeout = 30 * time.Second

// New returns a client for the core at rawURL. The URL must be absolute and
// http or https; any path on it is kept, so a core served under a prefix works
// unchanged.
func New(rawURL string) (*Client, error) {
	base, err := NormalizeURL(rawURL)
	if err != nil {
		return nil, err
	}
	return &Client{
		baseURL: base,
		http:    &http.Client{Timeout: DefaultTimeout},
	}, nil
}

// NormalizeURL validates a core URL and returns it without a trailing slash,
// so the same core is always spelled the same way in a stored context.
func NormalizeURL(rawURL string) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", errors.New("core: empty core URL")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("core: invalid core URL %q: %w", rawURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf(
			"core: core URL %q must be http or https", rawURL,
		)
	}
	if u.Host == "" {
		return "", fmt.Errorf("core: core URL %q has no host", rawURL)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

// BaseURL returns the normalized core URL the client talks to.
func (c *Client) BaseURL() string { return c.baseURL }

// Host returns the core's host:port, which is what a context is named after.
func (c *Client) Host() string {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return c.baseURL
	}
	return u.Host
}

// LoginURL is where the browser starts the login: the core's provider-login
// endpoint, carrying the loopback address to hand the result back to and the
// state that binds the callback to this process.
func (c *Client) LoginURL(provider, redirectURI, state string) string {
	q := url.Values{
		"redirect_uri": {redirectURI},
		"state":        {state},
	}
	return fmt.Sprintf(
		"%s/api/v1/auth/%s/login?%s",
		c.baseURL, url.PathEscape(provider), q.Encode(),
	)
}

// Refresh renews a login JWT from a refresh token. A rejected token reports
// ErrUnauthorized, which means the stored identity is dead and the user has to
// log in again.
func (c *Client) Refresh(
	ctx context.Context,
	refreshToken string,
) (Tokens, error) {
	body, err := json.Marshal(
		map[string]string{"refresh_token": refreshToken},
	)
	if err != nil {
		return Tokens{}, err
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/api/v1/auth/refresh",
		bytes.NewReader(body),
	)
	if err != nil {
		return Tokens{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	var out tokenResponse
	if err := c.do(req, &out); err != nil {
		return Tokens{}, err
	}
	return out.tokens(), nil
}

// Me describes the caller a login JWT names.
func (c *Client) Me(
	ctx context.Context,
	accessToken string,
) (Principal, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		c.baseURL+"/api/v1/auth/me",
		nil,
	)
	if err != nil {
		return Principal{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	var out meResponse
	if err := c.do(req, &out); err != nil {
		return Principal{}, err
	}
	return Principal(out), nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

func (t tokenResponse) tokens() Tokens {
	return Tokens{
		Access:    t.AccessToken,
		Refresh:   t.RefreshToken,
		ExpiresIn: time.Duration(t.ExpiresIn) * time.Second,
	}
}

type meResponse struct {
	UserID string `json:"user_id"`
	Handle string `json:"handle"`
}

// problemDetails is the RFC 7807 body every core error carries.
type problemDetails struct {
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

// do sends req and decodes a 2xx JSON body into out. A non-2xx response is
// turned into the core's own problem detail so the user reads the server's
// wording rather than a bare status code.
func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("core: %s: %w", req.URL.Host, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusError(resp.StatusCode, body)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("core: malformed response from %s: %w",
			req.URL.Host, err)
	}
	return nil
}

func statusError(status int, body []byte) error {
	msg := http.StatusText(status)
	var p problemDetails
	if err := json.Unmarshal(body, &p); err == nil {
		switch {
		case p.Detail != "":
			msg = p.Detail
		case p.Title != "":
			msg = p.Title
		}
	}
	if status == http.StatusUnauthorized {
		return fmt.Errorf("%w: %s", ErrUnauthorized, msg)
	}
	return fmt.Errorf("core: %s (HTTP %d)", msg, status)
}
