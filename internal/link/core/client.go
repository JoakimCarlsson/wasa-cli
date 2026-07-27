package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/joakimcarlsson/wasa-api/pkg/proto"
)

// DefaultProvider is the login provider used when none is named. The core
// selects its provider by path segment, so a second one drops in without a
// change here.
const DefaultProvider = "github"

// ErrUnauthorized reports that the core rejected the credential presented —
// an expired login JWT, or a refresh token that is unknown, expired or
// revoked. Callers turn it into "log in again" rather than a transport error.
var ErrUnauthorized = errors.New("core: unauthorized")

// ErrForbidden reports that the credential was accepted but names a principal
// without the authority asked for. It is not a stale login, so re-logging in
// does not help: the caller needs access granted.
var ErrForbidden = errors.New("core: forbidden")

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
//
// Every request goes out through pkg/proto, the client wasa-api generates from
// its own handler signatures, so an endpoint that changes shape breaks the
// build here instead of failing as a 4xx at runtime. What this type adds on
// top is the CLI's own vocabulary: a normalized core URL, per-call bearer
// tokens, and errors that read as ErrUnauthorized or ErrForbidden.
type Client struct {
	baseURL string
	http    *http.Client
	api     *proto.Client
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
	httpClient := &http.Client{Timeout: DefaultTimeout}
	api, err := proto.New(base, proto.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("core: %w", err)
	}
	return &Client{baseURL: base, http: httpClient, api: api}, nil
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
	out, err := c.api.PostApiV1AuthRefresh(
		ctx,
		proto.PostApiV1AuthRefreshParams{
			Body: proto.RefreshRequest{RefreshToken: refreshToken},
		},
	)
	if err != nil {
		return Tokens{}, c.asError(err)
	}
	tokens := Tokens{
		Access:    out.AccessToken,
		ExpiresIn: time.Duration(out.ExpiresIn) * time.Second,
	}
	if out.RefreshToken != nil {
		tokens.Refresh = *out.RefreshToken
	}
	return tokens, nil
}

// Me describes the caller a login JWT names.
func (c *Client) Me(
	ctx context.Context,
	accessToken string,
) (Principal, error) {
	out, err := c.bearer(accessToken).GetApiV1AuthMe(ctx)
	if err != nil {
		return Principal{}, c.asError(err)
	}
	p := Principal{UserID: out.UserID}
	if out.Handle != nil {
		p.Handle = *out.Handle
	}
	return p, nil
}

// bearer returns a client that presents accessToken on every call. The
// generated client takes its credentials at construction, and one core client
// serves calls for more than one token, so the authenticated variant is built
// per call — it shares the same *http.Client, so no connection pool is lost.
func (c *Client) bearer(accessToken string) *proto.Client {
	api, err := proto.New(
		c.baseURL,
		proto.WithHTTPClient(c.http),
		proto.WithHeader("Authorization", "Bearer "+accessToken),
	)
	if err != nil {
		return c.api
	}
	return api
}

// asError turns a generated-client error into the CLI's vocabulary. A response
// the core described as a problem detail is reported in the core's own wording
// so the user reads the server's explanation, and the two statuses a caller
// acts on differently — 401 relog, 403 no access — carry sentinel errors.
func (c *Client) asError(err error) error {
	var apiErr *proto.APIError
	if !errors.As(err, &apiErr) {
		return fmt.Errorf("core: %w", err)
	}
	msg := problemMessage(apiErr)
	switch apiErr.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: %s", ErrUnauthorized, msg)
	case http.StatusForbidden:
		return fmt.Errorf("%w: %s", ErrForbidden, msg)
	}
	return fmt.Errorf("core: %s (HTTP %d)", msg, apiErr.StatusCode)
}

// problemMessage picks the most specific wording an error response offers.
// The body is parsed even for a status the endpoint never declared, because a
// core behind a proxy can answer with a problem detail the spec does not list.
func problemMessage(apiErr *proto.APIError) string {
	pd, ok := proto.ErrorValue[*proto.ProblemDetails](apiErr)
	if !ok {
		pd = &proto.ProblemDetails{}
		if json.Unmarshal(apiErr.Body, pd) != nil {
			return http.StatusText(apiErr.StatusCode)
		}
	}
	switch {
	case pd.Detail != nil && *pd.Detail != "":
		return *pd.Detail
	case pd.Title != nil && *pd.Title != "":
		return *pd.Title
	}
	return http.StatusText(apiErr.StatusCode)
}
