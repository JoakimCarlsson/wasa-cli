// Package core talks to a wasa-api core: the browser login round-trip and the
// authenticated calls that follow it.
//
// The login flow is the standard CLI loopback pattern. wasa binds a transient
// listener on 127.0.0.1, hands the core that address as the redirect target,
// and opens the browser at the core's provider-login endpoint. The core drives
// the whole provider exchange and hands back only its own login JWT and
// refresh token — a provider access token never reaches this process.
//
// Nothing here runs on the offline/solo path.
package core
