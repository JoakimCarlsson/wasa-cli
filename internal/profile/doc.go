// Package profile resolves a workspace profile into the concrete environment
// injected into a session at launch. It loads the profile's env files from
// disk, merges them with the profile's inline env map and the per-program
// config-dir override, and returns the result as KEY=VALUE entries ready to
// hand to tmux as new-session -e arguments.
//
// Secrets live in env files and are read only here, at launch time; this
// package never writes them back into wasa's persisted state, which stores only
// the env-file paths.
package profile
