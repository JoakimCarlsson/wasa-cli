//go:build !windows

// Package gitremote is the git remote helper that carries refs/wasa/* to a
// wasa-api core: a `wasa://` remote is a normal git remote whose transport
// authenticates with a repo- and action-scoped token instead of an account
// credential.
//
// The transport itself is git's own smart HTTP. The helper resolves the URL to
// the core serving that repo and hands the whole conversation to
// `git remote-https`, so pack negotiation, protocol v2, redirects and the
// option protocol stay git's business and keep working as git evolves. What
// the helper adds is the credential: the child is pointed at a credential
// helper of wasa's own, which mints a token good for one repo and one action
// at the moment git asks for it.
//
// The direction is not known when git starts the conversation — git sends
// `capabilities` and its options before it says whether this is a fetch or a
// push — so the helper records the direction the first revealing command
// implies and the credential helper reads it back. A fetch therefore presents
// a pull-scoped token and a push a push-scoped one, and the core rejects the
// pair the other way round.
//
// Nothing here runs on the offline/solo path.
package gitremote
