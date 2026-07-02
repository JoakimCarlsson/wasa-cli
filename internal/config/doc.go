// Package config loads the optional cockpit configuration from
// $WASA_HOME/config.json and resolves it over the built-in defaults. It owns the
// schema for the three user-facing axes — theme (colours), keys (bindings) and
// layout (column sizing) — and validates a user file so a typo or a conflicting
// binding is reported at startup rather than silently mis-applied.
//
// The contract the cockpit relies on is back-compatibility: an absent or partial
// file resolves to exactly the built-in defaults, so zero config reproduces the
// historical appearance and key handling byte-for-byte. JSON is used to match the
// registry's on-disk format.
package config
