//go:build !windows

// Package identity is the local identity substrate for the control-plane path:
// a kubectl-style store of named contexts (core URL + principal + the keychain
// slot holding that identity's tokens) plus the token store behind those slots.
//
// It is a leaf package so both the CLI and the git-remote-wasa helper can read
// the same state; nothing here runs on the offline/solo path. Every file it
// owns is mode 0600, written temp+rename, and read or written under an
// exclusive flock held for the whole operation, because a foreground command
// and a background push can touch it at once.
package identity
