//go:build !windows

package gitremote

import (
	"fmt"
	"strings"

	"github.com/joakimcarlsson/wasa-cli/internal/link/core"
)

// Scheme is the URL scheme git routes to this helper. git resolves it by
// looking for an executable named git-remote-<scheme> on PATH.
const Scheme = "wasa"

// BinaryName is the name git looks that executable up under. wasa answers to
// it when invoked under it, so installing the helper is one symlink.
const BinaryName = "git-remote-" + Scheme

// repoIDLen is the length of a repo id: the core names repos with ULIDs, and
// the length is what separates a one-segment remote naming an id from a
// malformed one.
const repoIDLen = 26

// repoIDAlphabet is Crockford base32, the alphabet a ULID is spelled in. I, L,
// O and U are absent by design, so a typo in an id is caught here rather than
// as a 404 from the core.
const repoIDAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Target is the repo a wasa:// remote names.
//
// Audience and URL are the same string twice over — the audience is the path
// the core serves the repo at — which is the point: the remote a transfer
// targets and the audience of the token that opens it cannot drift apart.
type Target struct {
	// Audience is what a repo-scoped token is minted for.
	Audience string
	// URL is the https endpoint on the core serving that repo over smart HTTP.
	URL string
}

// Resolve turns the URL git handed the helper into the core endpoint it
// addresses and the audience a token for it is minted for. coreURL is the
// normalized core of the login this transfer acts as.
//
// Two forms are accepted, matching the two audience shapes the core parses:
// wasa://<owner>/<repo> for a remote spelled the way a person writes it, and
// wasa://<repo-id> once the repo's id is known. A trailing .git is dropped
// from either, as git itself does.
func Resolve(coreURL, rawURL string) (Target, error) {
	if coreURL == "" {
		return Target{}, fmt.Errorf("gitremote: no core to reach %q", rawURL)
	}
	base, err := core.NormalizeURL(coreURL)
	if err != nil {
		return Target{}, err
	}

	audience, err := audienceOf(rawURL)
	if err != nil {
		return Target{}, err
	}
	return Target{Audience: audience, URL: base + audience}, nil
}

// audienceOf parses a wasa remote into the audience it names. It accepts the
// address with or without the scheme, because git strips the scheme itself for
// a remote written in the wasa::<address> transport form.
func audienceOf(rawURL string) (string, error) {
	spec := strings.TrimSpace(rawURL)
	for _, prefix := range []string{Scheme + "://", Scheme + ":"} {
		if trimmed, ok := strings.CutPrefix(spec, prefix); ok {
			spec = trimmed
			break
		}
	}
	spec = strings.Trim(spec, "/")

	segments := strings.Split(spec, "/")
	for i, s := range segments {
		if i == len(segments)-1 {
			s = strings.TrimSuffix(s, ".git")
			segments[i] = s
		}
		if !validSegment(s) {
			return "", badRemote(rawURL)
		}
	}

	switch len(segments) {
	case 1:
		if !validRepoID(segments[0]) {
			return "", badRemote(rawURL)
		}
		return core.RepoAudience(segments[0]), nil
	case 2:
		return core.SlugAudience(segments[0], segments[1]), nil
	default:
		return "", badRemote(rawURL)
	}
}

func badRemote(rawURL string) error {
	return fmt.Errorf(
		"gitremote: %q is not a wasa remote — "+
			"use %s://<owner>/<repo> or %s://<repo-id>",
		rawURL, Scheme, Scheme,
	)
}

// validSegment reports whether a path segment is safe to place in a URL the
// core routes on. The segment reaches the core as a path element, so anything
// that could climb out of it or split it is refused here rather than being
// escaped and sent on to be resolved as some other repo.
func validSegment(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '.', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// validRepoID reports whether s is spelled like a repo id, which is what makes
// a one-segment remote an id rather than half of a slug.
func validRepoID(s string) bool {
	if len(s) != repoIDLen {
		return false
	}
	for _, c := range strings.ToUpper(s) {
		if !strings.ContainsRune(repoIDAlphabet, c) {
			return false
		}
	}
	return true
}
