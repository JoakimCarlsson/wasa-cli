//go:build !windows

// Package auth is the seam between the identity store and a wasa-api core: it
// records what a completed login produced, and hands later commands a login
// JWT that is known to still be valid.
//
// Every authenticated command goes through Access, so refresh-ahead happens in
// exactly one place: a token inside identity.RefreshBuffer of expiry is renewed
// against the core and written back before it is handed out.
//
// Nothing here runs on the offline/solo path.
package auth
