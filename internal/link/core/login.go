package core

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"time"
)

// LoginTimeout bounds how long wasa waits for the browser round-trip before
// giving the terminal back.
const LoginTimeout = 5 * time.Minute

// callbackPath is the single path the loopback listener answers on.
const callbackPath = "/callback"

// LoginOptions tunes one login round-trip. The zero value is the normal
// interactive login against the default provider.
type LoginOptions struct {
	// Provider names the core's login provider. Empty means DefaultProvider.
	Provider string
	// Timeout bounds the wait for the callback. Zero means LoginTimeout.
	Timeout time.Duration
	// Prompt receives the URL the browser is being sent to, so the user can
	// open it by hand when the browser cannot be launched. A nil Prompt is
	// silent.
	Prompt io.Writer
	// OpenBrowser launches the user's browser. Nil uses the host's opener;
	// tests substitute their own.
	OpenBrowser func(url string) error
}

// Login runs the loopback OAuth round-trip against the core and returns the
// login JWT and refresh token it hands back.
//
// The listener is bound before the browser is opened, so the port in the
// redirect target is known to be ours, and it is torn down as soon as the
// callback lands. The state parameter is compared in constant time: a callback
// that does not carry this process's state is refused rather than trusted.
func Login(
	ctx context.Context,
	c *Client,
	opts LoginOptions,
) (Tokens, error) {
	provider := opts.Provider
	if provider == "" {
		provider = DefaultProvider
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = LoginTimeout
	}
	open := opts.OpenBrowser
	if open == nil {
		open = OpenBrowser
	}

	state, err := randomState()
	if err != nil {
		return Tokens{}, err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Tokens{}, fmt.Errorf("core: loopback listener: %w", err)
	}
	defer listener.Close()

	redirectURI := "http://" + listener.Addr().String() + callbackPath
	results := make(chan callbackResult, 1)
	srv := &http.Server{
		Handler:           callbackHandler(state, results),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = srv.Serve(listener) }()
	defer func() {
		shutdown, cancel := context.WithTimeout(
			context.Background(), 2*time.Second,
		)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	loginURL := c.LoginURL(provider, redirectURI, state)
	if opts.Prompt != nil {
		fmt.Fprintf(opts.Prompt, "Opening %s\n", loginURL)
	}
	if err := open(loginURL); err != nil && opts.Prompt != nil {
		fmt.Fprintf(
			opts.Prompt,
			"wasa: could not open a browser (%v); open the URL above\n",
			err,
		)
	}

	wait, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case res := <-results:
		return res.tokens, res.err
	case <-wait.Done():
		if errors.Is(ctx.Err(), context.Canceled) {
			return Tokens{}, ctx.Err()
		}
		return Tokens{}, fmt.Errorf(
			"core: timed out after %s waiting for the browser to complete "+
				"the login", timeout,
		)
	}
}

type callbackResult struct {
	tokens Tokens
	err    error
}

// callbackHandler answers the core's redirect back to the loopback listener.
// It reports the first callback it accepts on results and always renders a
// page, so the browser tab says what happened instead of failing to connect.
func callbackHandler(
	state string,
	results chan<- callbackResult,
) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		res := parseCallback(state, r.URL.Query())
		select {
		case results <- res:
		default:
		}
		writeCallbackPage(w, res.err)
	})
	return mux
}

// parseCallback reads the core's redirect. The core hands back either its own
// token pair or an error it already rendered for the user; anything whose
// state does not match this process is not ours to act on.
func parseCallback(state string, q url.Values) callbackResult {
	if got := q.Get("state"); !constantTimeEqual(state, got) {
		return callbackResult{err: errors.New(
			"core: the login callback state does not match this login",
		)}
	}
	if e := q.Get("error"); e != "" {
		detail := q.Get("error_description")
		if detail == "" {
			detail = e
		}
		return callbackResult{
			err: fmt.Errorf("core: login failed: %s", detail),
		}
	}
	access := q.Get("access_token")
	if access == "" {
		return callbackResult{err: errors.New(
			"core: the login callback carried no access token",
		)}
	}
	var expiresIn time.Duration
	if secs, err := strconv.Atoi(q.Get("expires_in")); err == nil && secs > 0 {
		expiresIn = time.Duration(secs) * time.Second
	}
	return callbackResult{tokens: Tokens{
		Access:    access,
		Refresh:   q.Get("refresh_token"),
		ExpiresIn: expiresIn,
	}}
}

func constantTimeEqual(want, got string) bool {
	if want == "" || got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}

func writeCallbackPage(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	body, status := "You are logged in to wasa. You can close this tab.",
		http.StatusOK
	if err != nil {
		body, status = "wasa login failed: "+err.Error(), http.StatusBadRequest
	}
	w.WriteHeader(status)
	fmt.Fprintf(
		w,
		"<!doctype html><meta charset=utf-8><title>wasa</title><p>%s</p>",
		html.EscapeString(body),
	)
}

func randomState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// OpenBrowser launches the host's default browser on rawURL. It starts the
// opener and does not wait for it: a browser that stays open must not hold the
// login flow.
func OpenBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
