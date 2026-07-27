//go:build !windows

// Package userdirs resolves wasa's global config and cache directories. It is
// the single place those paths are derived — no caller spells out
// ~/.config/wasa. Under `go test` both resolve to a throwaway per-process
// directory so a test can neither read nor pollute real user state.
package userdirs
