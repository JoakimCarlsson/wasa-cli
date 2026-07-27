//go:build !windows

// Package repotoken is the per-repo credential cache: it trades the account's
// login JWT for the short-lived, repo- and action-scoped tokens git operations
// present, and keeps each one only as long as it is good for.
//
// Nothing outside this package ever hands a login JWT to a git host. A push to
// one repo gets a token that can push to that repo and nothing else, so a
// credential captured by a host — or left in a git trace — is worth one repo
// for a few minutes rather than the whole account.
//
// A Cache is safe for concurrent use, and is meant to be shared: a fan-out
// across repos exchanges in parallel, while several callers racing for the same
// (audience, action) collapse onto a single exchange.
//
// Nothing here runs on the offline/solo path.
package repotoken
