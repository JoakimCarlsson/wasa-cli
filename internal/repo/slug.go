package repo

import "strings"

// Slug derives the <owner>/<repo> a git remote URL names, which is how the
// control plane addresses a repository. It handles the two spellings that
// carry a host — scheme URLs (https://, ssh://, git://) and the scp-like
// git@host:owner/repo form — and returns an empty string for anything else: a
// local path, or a URL with no owner segment, names no owner to derive one
// from. A caller that needs a slug then has to be given one rather than
// having one guessed for it.
func Slug(remoteURL string) string {
	spec := strings.TrimSpace(remoteURL)
	if spec == "" {
		return ""
	}
	switch _, rest, hasScheme := strings.Cut(spec, "://"); {
	case hasScheme:
		_, after, ok := strings.Cut(rest, "/")
		if !ok {
			return ""
		}
		spec = after
	default:
		host, path, ok := strings.Cut(spec, ":")
		if !ok || host == "" || strings.Contains(host, "/") {
			return ""
		}
		spec = path
	}

	spec = strings.Trim(spec, "/")
	spec = strings.TrimSuffix(spec, ".git")
	segments := strings.Split(spec, "/")
	if len(segments) < 2 {
		return ""
	}
	owner := segments[len(segments)-2]
	name := segments[len(segments)-1]
	if owner == "" || name == "" {
		return ""
	}
	return owner + "/" + name
}
