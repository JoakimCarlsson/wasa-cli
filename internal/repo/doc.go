// Package repo is the shared seam that resolves a directory to the git
// repository identity wasa registers as a workspace. Both the CLI (in-repo
// launch and workspace add) and the cockpit create flow route through it so a
// repository always resolves to the same canonical path, remote URL and
// therefore the same content-addressed workspace id, regardless of how it was
// reached. Duplicating this resolution is what would let the id drift into a
// second workspace for the same repository.
package repo
